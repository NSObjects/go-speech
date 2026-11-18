// Package tencent implements Tencent Cloud Speech Recognition WebSocket V2
// using the provider-independent asr.Stream interface. Audio is passed through
// unchanged. Tencent's proprietary Opus framing is NOT Ogg Opus.
package tencent

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/gorilla/websocket"
)

const (
	DefaultEndpoint    = "wss://asr.cloud.tencent.com"
	DefaultEngineModel = "16k_zh"
	defaultStartTimeout = 15 * time.Second
	defaultWriteTimeout = 10 * time.Second
)

// Config holds Tencent Cloud ASR credentials and V2 engine selection.
// AppID is the numeric Tencent Cloud application ID, not a model identifier.
// Each OpenStream generates fresh voice_id, nonce, timestamp and signature.
// Endpoint is a host-only URL; ws is allowed only for loopback tests.
type Config struct {
	AppID        string
	SecretID     string
	SecretKey    string
	EngineModel  string
	Endpoint     string
	StartTimeout time.Duration
	NeedVAD      bool
	Dialer       *websocket.Dialer
}

type Client struct{ cfg Config }

var _ asr.StreamRecognizer = (*Client)(nil)

func New(cfg Config) (*Client, error) {
	appID, err := strconv.ParseUint(cfg.AppID, 10, 64)
	if err != nil || appID == 0 || strings.TrimSpace(cfg.SecretID) == "" || strings.TrimSpace(cfg.SecretKey) == "" {
		return nil, argumentError("a positive AppID, SecretID and SecretKey are required")
	}
	if cfg.EngineModel == "" {
		cfg.EngineModel = DefaultEngineModel
	}
	if strings.TrimSpace(cfg.EngineModel) != cfg.EngineModel || strings.ContainsAny(cfg.EngineModel, "&?=/# 	
") {
		return nil, argumentError("invalid EngineModel")
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = DefaultEndpoint
	}
	if cfg.StartTimeout < 0 {
		return nil, argumentError("StartTimeout must be nonnegative")
	}
	if cfg.StartTimeout == 0 {
		cfg.StartTimeout = defaultStartTimeout
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Path != "" && u.Path != "/") {
		return nil, argumentError("Endpoint must be a WebSocket origin without path or query")
	}
	if u.Scheme != "wss" && !(u.Scheme == "ws" && isLoopback(u.Hostname())) {
		return nil, argumentError("wss is required outside loopback tests")
	}
	if cfg.Dialer != nil {
		copyDialer := *cfg.Dialer
		cfg.Dialer = &copyDialer
	}
	return &Client{cfg: cfg}, nil
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// SupportedAudio reports the PCM transport supported by the selected engine.
// The Tencent voice_format=10 Opus protocol uses proprietary frame prefixes,
// not audio.EncodingOpus's Ogg container, so it is deliberately not advertised.
func SupportedAudio(engine string) audio.Capability {
	rate := 16000
	if strings.HasPrefix(engine, "8k_") {
		rate = 8000
	}
	return audio.Capability{
		Encodings: []audio.Encoding{audio.EncodingPCM16LE},
		SampleRates: []int{rate},
		Channels: []int{1},
	}
}

func (c *Client) OpenStream(ctx context.Context, req asr.Config) (asr.Stream, error) {
	if c == nil {
		return nil, argumentError("nil client")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := req.Format.Validate(); err != nil {
		return nil, &speecherr.Error{Code: speecherr.CodeInvalidArgument, Provider: "tencent", Operation: "start", Err: err}
	}
	if err := SupportedAudio(c.cfg.EngineModel).Validate(req.Format); err != nil {
		return nil, &speecherr.Error{Code: speecherr.CodeUnsupported, Provider: "tencent", Operation: "start", Err: err}
	}
	if req.Language != "" {
		return nil, unsupportedError("language is selected by EngineModel; per-request Language is not supported")
	}
	if len(req.Hints) > 0 {
		return nil, unsupportedError("per-request Hints are not yet supported")
	}
	voiceID, err := requestID()
	if err != nil {
		return nil, &speecherr.Error{Code: speecherr.CodeInternal, Provider: "tencent", Operation: "voice_id", Err: err}
	}
	var entropy [4]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return nil, &speecherr.Error{Code: speecherr.CodeInternal, Provider: "tencent", Operation: "nonce", Err: err}
	}
	nonce := 1 + binary.BigEndian.Uint32(entropy[:])%999999999
	target := c.signedURL(time.Now(), nonce, voiceID)
	dialer := *websocket.DefaultDialer
	if c.cfg.Dialer != nil {
		dialer = *c.cfg.Dialer
	}
	if dialer.HandshakeTimeout == 0 {
		dialer.HandshakeTimeout = c.cfg.StartTimeout
	}
	conn, response, err := dialer.DialContext(ctx, target, nil)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		return nil, connectError(err, response)
	}
	conn.SetReadLimit(1 << 20)
	s := newStream(conn, ctx, voiceID)
	if err := s.awaitHandshake(ctx, c.cfg.StartTimeout); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

// signedURL signs the host + request path + lexically sorted query exactly as
// required by the Tencent ASR V2 WebSocket protocol. Never log this URL: the
// signature and SecretID are credentials.
func (c *Client) signedURL(now time.Time, nonce uint32, voiceID string) string {
	u, _ := url.Parse(c.cfg.Endpoint) // validated by New
	u.Path = "/asr/v2/" + c.cfg.AppID
	params := url.Values{}
	params.Set("secretid", c.cfg.SecretID)
	params.Set("timestamp", strconv.FormatInt(now.Unix(), 10))
	params.Set("expired", strconv.FormatInt(now.Add(time.Hour).Unix(), 10))
	params.Set("nonce", strconv.FormatUint(uint64(nonce), 10))
	params.Set("engine_model_type", c.cfg.EngineModel)
	params.Set("voice_id", voiceID)
	params.Set("voice_format", "1")
	if c.cfg.NeedVAD {
		params.Set("needvad", "1")
	} else {
		params.Set("needvad", "0")
	}
	// URL query encoding is deterministic and sorts keys by key name.
	query := params.Encode()
	mac := hmac.New(sha1.New, []byte(c.cfg.SecretKey))
	_, _ = mac.Write([]byte(u.Host + u.Path + "?" + query))
	params.Set("signature", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	u.RawQuery = params.Encode()
	return u.String()
}

func requestID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	id[6] = id[6]&15 | 0x40
	id[8] = id[8]&63 | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:]), nil
}

func argumentError(message string) error {
	return &speecherr.Error{Code: speecherr.CodeInvalidArgument, Provider: "tencent", Operation: "configure", Err: errors.New(message)}
}
func unsupportedError(message string) error {
	return &speecherr.Error{Code: speecherr.CodeUnsupported, Provider: "tencent", Operation: "start", Err: errors.New(message)}
}
func protocolError(message string) error {
	return &speecherr.Error{Code: speecherr.CodeProtocol, Provider: "tencent", Operation: "recv", Err: errors.New(message)}
}

// connectError deliberately does not expose the signed WebSocket URL (whose
// query contains an authentication signature and SecretID) via error strings.
func connectError(err error, resp *http.Response) error {
	code := speecherr.CodeUnavailable
	message := "WebSocket connection failed"
	if resp != nil {
		message = fmt.Sprintf("WebSocket handshake HTTP %d", resp.StatusCode)
		switch resp.StatusCode {
		case http.StatusBadRequest:
			code = speecherr.CodeInvalidArgument
		case http.StatusUnauthorized, http.StatusForbidden:
			code = speecherr.CodeUnauthorized
		case http.StatusTooManyRequests:
			code = speecherr.CodeRateLimited
		}
	} else {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			code = speecherr.CodeTimeout
		}
	}
	return &speecherr.Error{
		Code: code, Provider: "tencent", Operation: "connect",
		Err: errors.New(message),
		Retryable: code == speecherr.CodeUnavailable || code == speecherr.CodeTimeout || code == speecherr.CodeRateLimited,
	}
}
