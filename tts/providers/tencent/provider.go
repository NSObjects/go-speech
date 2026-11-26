// Package tencent implements Tencent Cloud streaming text-to-speech
// (TextToStreamAudioWSv2). The adapter does not transcode or play audio.
package tencent

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/NSObjects/go-speech/tts"
	"github.com/gorilla/websocket"
)

const (
	defaultEndpoint     = "wss://tts.cloud.tencent.com/stream_wsv2"
	defaultStartTimeout = 15 * time.Second
	maxTextRunes        = 10000
	maxBatchAudio       = 64 << 20
)

var (
	_ tts.Synthesizer       = (*Client)(nil)
	_ tts.StreamSynthesizer = (*Client)(nil)
	_ tts.DuplexSynthesizer = (*Client)(nil)
)

// Config contains Tencent TTS credentials and optional transport settings.
// The WebSocket v2 request uses AppID, SecretID and SecretKey, not TC3-HMAC.
type Config struct {
	AppID        string
	SecretID     string
	SecretKey    string
	Endpoint     string
	StartTimeout time.Duration
	Dialer       *websocket.Dialer
}

type Client struct{ cfg Config }

func New(cfg Config) (*Client, error) {
	if _, err := strconv.ParseUint(cfg.AppID, 10, 64); err != nil || cfg.AppID == "" {
		return nil, invalid("AppID must be a positive decimal account ID")
	}
	if strings.TrimSpace(cfg.SecretID) == "" || strings.TrimSpace(cfg.SecretKey) == "" {
		return nil, invalid("SecretID and SecretKey are required")
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = defaultEndpoint
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		u.Path != "/stream_wsv2" || (u.Scheme != "wss" && !(u.Scheme == "ws" && isLoopback(u.Hostname()))) {
		return nil, invalid("Endpoint must be a secure /stream_wsv2 WebSocket URL")
	}
	if cfg.StartTimeout < 0 {
		return nil, invalid("StartTimeout cannot be negative")
	}
	if cfg.StartTimeout == 0 {
		cfg.StartTimeout = defaultStartTimeout
	}
	if cfg.Dialer != nil {
		copy := *cfg.Dialer
		cfg.Dialer = &copy
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

// SupportedAudio returns the protocol-wide PCM/MP3 output options. Specific
// voices may support fewer sample rates; Tencent validates that combination.
func SupportedAudio() audio.Capability {
	return audio.Capability{
		Encodings:   []audio.Encoding{audio.EncodingPCM16LE, audio.EncodingMP3},
		SampleRates: []int{8000, 16000, 24000},
		Channels:    []int{1},
	}
}

func validateRequest(req tts.Request) (tts.Request, string, error) {
	switch req.TextFormat {
	case "", tts.TextPlain:
	case tts.TextSSML, tts.TextInlineTag:
		return req, "", &speecherr.Error{Code: speecherr.CodeUnsupported, Provider: "tencent", Operation: "tts", Err: tts.ErrUnsupportedTextFormat}
	default:
		return req, "", invalid("unknown text format")
	}
	if !utf8.ValidString(req.Text) || strings.TrimSpace(req.Text) == "" ||
		utf8.RuneCountInString(req.Text) > maxTextRunes {
		return req, "", invalid("Text must contain 1–10000 valid UTF-8 characters")
	}
	if _, err := strconv.ParseUint(req.Voice, 10, 64); err != nil || req.Voice == "" {
		return req, "", invalid("Voice must be a numeric Tencent VoiceType")
	}
	if req.Format == (audio.Format{}) {
		req.Format = audio.Format{Encoding: audio.EncodingMP3, SampleRate: 16000, Channels: 1}
	}
	if err := req.Format.Validate(); err != nil {
		return req, "", &speecherr.Error{Code: speecherr.CodeInvalidArgument, Provider: "tencent", Operation: "tts", Err: err}
	}
	if err := SupportedAudio().Validate(req.Format); err != nil {
		return req, "", &speecherr.Error{Code: speecherr.CodeUnsupported, Provider: "tencent", Operation: "tts", Err: err}
	}
	if req.Format.Encoding == audio.EncodingPCM16LE {
		return req, "pcm", nil
	}
	return req, "mp3", nil
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&15 | 0x40
	b[8] = b[8]&63 | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// signedEndpoint signs the canonical, sorted, unescaped query values. Only
// Signature itself is URL-encoded; never log or return this signed URL.
func signedEndpoint(cfg Config, req tts.Request, codec, sessionID string, now time.Time) (string, error) {
	u, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return "", err
	}
	q := url.Values{
		"Action":     {"TextToStreamAudioWSv2"},
		"AppId":      {cfg.AppID},
		"SecretId":   {cfg.SecretID},
		"Timestamp":  {strconv.FormatInt(now.Unix(), 10)},
		"Expired":    {strconv.FormatInt(now.Add(time.Hour).Unix(), 10)},
		"SessionId":  {sessionID},
		"VoiceType":  {req.Voice},
		"Codec":      {codec},
		"SampleRate": {strconv.Itoa(req.Format.SampleRate)},
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+q.Get(k))
	}
	payload := "GET" + u.Host + u.Path + "?" + strings.Join(pairs, "&")
	mac := hmac.New(sha1.New, []byte(cfg.SecretKey))
	_, _ = mac.Write([]byte(payload))
	q.Set("Signature", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (c *Client) SynthesizeStream(ctx context.Context, req tts.Request) (tts.Stream, error) {
	if c == nil {
		return nil, invalid("nil client")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	req, codec, err := validateRequest(req)
	if err != nil {
		return nil, err
	}
	sessionID, err := newID()
	if err != nil {
		return nil, &speecherr.Error{Code: speecherr.CodeInternal, Provider: "tencent", Operation: "id", Err: err}
	}
	endpoint, err := signedEndpoint(c.cfg, req, codec, sessionID, time.Now())
	if err != nil {
		return nil, err
	}
	dialer := *websocket.DefaultDialer
	if c.cfg.Dialer != nil {
		dialer = *c.cfg.Dialer
	}
	if dialer.HandshakeTimeout == 0 {
		dialer.HandshakeTimeout = c.cfg.StartTimeout
	}
	conn, response, err := dialer.DialContext(ctx, endpoint, nil)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, dialFailure(err, response)
	}
	conn.SetReadLimit(8 << 20)
	s := newStream(conn, ctx, sessionID, req.Format)
	if err := s.start(ctx, req.Text, c.cfg.StartTimeout); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

// OpenDuplex opens a Tencent TTS v2 session for incremental text input.
func (c *Client) OpenDuplex(ctx context.Context, cfg tts.DuplexRequest) (tts.DuplexStream, error) {
	if c == nil {
		return nil, invalid("nil client")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	req, codec, err := validateRequest(tts.Request{Text: "validation", Voice: cfg.Voice, Format: cfg.Format, TextFormat: cfg.TextFormat})
	if err != nil {
		return nil, err
	}
	sessionID, err := newID()
	if err != nil {
		return nil, &speecherr.Error{Code: speecherr.CodeInternal, Provider: "tencent", Operation: "id", Err: err}
	}
	endpoint, err := signedEndpoint(c.cfg, req, codec, sessionID, time.Now())
	if err != nil {
		return nil, err
	}
	dialer := *websocket.DefaultDialer
	if c.cfg.Dialer != nil {
		dialer = *c.cfg.Dialer
	}
	if dialer.HandshakeTimeout == 0 {
		dialer.HandshakeTimeout = c.cfg.StartTimeout
	}
	conn, response, err := dialer.DialContext(ctx, endpoint, nil)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, dialFailure(err, response)
	}
	conn.SetReadLimit(8 << 20)
	s := newStream(conn, ctx, sessionID, req.Format)
	if err := s.startReady(ctx, c.cfg.StartTimeout); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

func (c *Client) Synthesize(ctx context.Context, req tts.Request) (audio.Buffer, error) {
	stream, err := c.SynthesizeStream(ctx, req)
	if err != nil {
		return audio.Buffer{}, err
	}
	defer stream.Close()
	var out audio.Buffer
	for {
		chunk, err := stream.Recv(ctx)
		if errors.Is(err, io.EOF) {
			if len(out.Data) == 0 {
				return audio.Buffer{}, protocol("synthesis completed without audio")
			}
			return out, nil
		}
		if err != nil {
			return audio.Buffer{}, err
		}
		if len(chunk.Data) > maxBatchAudio-len(out.Data) {
			return audio.Buffer{}, &speecherr.Error{Code: speecherr.CodeInternal, Provider: "tencent", Operation: "tts", Err: errors.New("batch audio exceeds 64 MiB; use SynthesizeStream")}
		}
		if out.Format == (audio.Format{}) {
			out.Format = chunk.Format
		}
		out.Data = append(out.Data, chunk.Data...)
	}
}

func invalid(msg string) error {
	return &speecherr.Error{Code: speecherr.CodeInvalidArgument, Provider: "tencent", Operation: "tts", Err: errors.New(msg)}
}
func protocol(msg string) error {
	return &speecherr.Error{Code: speecherr.CodeProtocol, Provider: "tencent", Operation: "tts", Err: errors.New(msg)}
}
func dialFailure(err error, response *http.Response) error {
	code := speecherr.CodeUnavailable
	if response != nil {
		switch response.StatusCode {
		case http.StatusBadRequest:
			code = speecherr.CodeInvalidArgument
		case http.StatusUnauthorized, http.StatusForbidden:
			code = speecherr.CodeUnauthorized
		case http.StatusTooManyRequests:
			code = speecherr.CodeRateLimited
		}
		err = fmt.Errorf("WebSocket handshake HTTP %d: %w", response.StatusCode, err)
	}
	return &speecherr.Error{Code: code, Provider: "tencent", Operation: "connect", Err: err,
		Retryable: code == speecherr.CodeUnavailable || code == speecherr.CodeRateLimited}
}

type responseEvent struct {
	Code      *int   `json:"code"`
	Message   string `json:"message"`
	SessionID string `json:"session_id"`
	Ready     int    `json:"ready"`
	Final     int    `json:"final"`
	Heartbeat int    `json:"heartbeat"`
}

func decodeEvent(data []byte, sessionID string) (responseEvent, error) {
	var ev responseEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return ev, protocol("invalid response JSON")
	}
	if ev.Code == nil || ev.SessionID != sessionID {
		return ev, protocol("missing code or incorrect session_id")
	}
	if *ev.Code != 0 {
		return ev, remoteError(*ev.Code, ev.Message)
	}
	if (ev.Ready != 0 && ev.Ready != 1) || (ev.Final != 0 && ev.Final != 1) {
		return ev, protocol("invalid response ready/final flag")
	}
	return ev, nil
}
func remoteError(remote int, message string) error {
	code, retryable := speecherr.CodeInternal, false
	switch remote {
	case 10001, 10006, 10007, 10008:
		code = speecherr.CodeInvalidArgument
	case 10002:
		code, retryable = speecherr.CodeRateLimited, true
	case 3022:
		code = speecherr.CodeRateLimited // provider: resource pack exhausted; requires billing remediation
	case 10003:
		code = speecherr.CodeUnauthorized
	case 10004, 10009:
		code = speecherr.CodeTimeout
	case 10005:
		code = speecherr.CodeUnavailable
	case 20000, 20001, 20002:
		code, retryable = speecherr.CodeUnavailable, true
	case 20003:
		code, retryable = speecherr.CodeTimeout, true
	}
	return &speecherr.Error{Code: code, Provider: "tencent", Operation: "tts", RemoteCode: strconv.Itoa(remote), Retryable: retryable,
		Err: fmt.Errorf("Tencent TTS error %d: %s", remote, message)}
}
