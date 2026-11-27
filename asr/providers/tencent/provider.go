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
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/internal/errclass"
	"github.com/NSObjects/go-speech/internal/wireutil"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/gorilla/websocket"
)

const (
	DefaultEndpoint     = "wss://asr.cloud.tencent.com"
	DefaultEngineModel  = "16k_zh"
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
	// ReadIdleTimeout bounds a single Recv wait without a message.
	// Zero (default) disables the SDK idle timeout; the caller's context still applies.
	ReadIdleTimeout time.Duration
	NeedVAD         bool
	// VADSilenceTime optionally overrides server-side silence segmentation.
	// Requires NeedVAD. Zero uses Tencent's default (1000ms).
	VADSilenceTime time.Duration
	// MaxSpeakTime optionally forces final sentence segmentation during
	// continuous speech. Zero uses Tencent's default (60000ms).
	MaxSpeakTime time.Duration
	// HotwordID selects a Tencent-managed hotword table; empty uses the account default.
	HotwordID string
	// InputSampleRate=8000 explicitly accepts 8 kHz PCM for a 16 kHz engine.
	// Tencent performs the upsampling; the SDK does not convert audio.
	InputSampleRate int
	Dialer          *websocket.Dialer
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
	if strings.TrimSpace(cfg.EngineModel) != cfg.EngineModel || strings.ContainsAny(cfg.EngineModel, "&?=/# \t\r\n") {
		return nil, argumentError("invalid EngineModel")
	}
	if cfg.InputSampleRate != 0 && (cfg.InputSampleRate != 8000 || !strings.HasPrefix(cfg.EngineModel, "16k_")) {
		return nil, argumentError("InputSampleRate supports only 8000 with a 16k_ engine")
	}
	if cfg.HotwordID != "" && (len(cfg.HotwordID) > 128 || strings.ContainsAny(cfg.HotwordID, "&?=/# \t\r\n")) {
		return nil, argumentError("invalid HotwordID")
	}
	if err := validateSegmentation(cfg); err != nil {
		return nil, err
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = DefaultEndpoint
	}
	if cfg.StartTimeout < 0 {
		return nil, argumentError("StartTimeout must be nonnegative")
	}
	if cfg.ReadIdleTimeout < 0 {
		return nil, argumentError("ReadIdleTimeout must be nonnegative")
	}
	if cfg.StartTimeout == 0 {
		cfg.StartTimeout = defaultStartTimeout
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Path != "" && u.Path != "/") {
		return nil, argumentError("Endpoint must be a WebSocket origin without path or query")
	}
	if u.Scheme != "wss" && !(u.Scheme == "ws" && wireutil.IsLoopback(u.Hostname())) {
		return nil, argumentError("wss is required outside loopback tests")
	}
	if cfg.Dialer != nil {
		copyDialer := *cfg.Dialer
		cfg.Dialer = &copyDialer
	}
	return &Client{cfg: cfg}, nil
}

// SupportedAudio reports locally implemented formats for the selected engine.
// Native framed Opus, MP3 and AAC are currently restricted to the 16k_zh model.
func SupportedAudio(engine string) audio.Capability {
	rate := 16000
	if strings.HasPrefix(engine, "8k_") {
		rate = 8000
	}
	encodings := []audio.Encoding{audio.EncodingPCM16LE, audio.EncodingWAV}
	if engine == "16k_zh" {
		encodings = append(encodings, EncodingOpusFrames, audio.EncodingMP3, audio.EncodingAAC)
	}
	if engine == "Hy-ASR-3.0-preview" {
		encodings = []audio.Encoding{audio.EncodingPCM16LE}
	}
	return audio.Capability{
		Encodings:   encodings,
		SampleRates: []int{rate},
		Channels:    []int{1},
	}
}

func (c *Client) OpenStream(ctx context.Context, req asr.Config) (asr.Stream, error) {
	if c == nil {
		return nil, argumentError("nil client")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	effective, native, extra, hotwords, err := c.prepare(req)
	if err != nil {
		return nil, err
	}
	voiceID, err := wireutil.UUIDv4()
	if err != nil {
		return nil, &speecherr.Error{Code: speecherr.CodeInternal, Provider: "tencent", Operation: "voice_id", Err: err}
	}
	var entropy [4]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return nil, &speecherr.Error{Code: speecherr.CodeInternal, Provider: "tencent", Operation: "nonce", Err: err}
	}
	nonce := 1 + binary.BigEndian.Uint32(entropy[:])%999999999
	target := effective.signedURLWithOptions(time.Now(), nonce, voiceID, hotwords, extra)
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
	s.encoding = req.Format.Encoding
	s.details = req.Options.UtteranceDetails || enabledEmotion(native) || (native.WordInfo != nil && *native.WordInfo != 0)
	s.returnEmpty = native.ReturnEmptyResults != nil && *native.ReturnEmptyResults
	s.readIdleTimeout = c.cfg.ReadIdleTimeout
	if err := s.awaitHandshake(ctx, c.cfg.StartTimeout); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

// signedURL signs the host + request path + lexically sorted query exactly as
// required by Tencent. The HMAC input contains UNESCAPED query values (including
// Chinese hotwords), while the actual request URL percent-encodes those values.
// Never log the signed URL: it contains SecretID, hotwords and signature.
func (c *Client) signedURL(now time.Time, nonce uint32, voiceID string) string {
	return c.signedURLWithHints(now, nonce, voiceID, "")
}

func (c *Client) signedURLWithHints(now time.Time, nonce uint32, voiceID, hotwords string) string {
	return c.signedURLWithOptions(now, nonce, voiceID, hotwords, nil)
}

func (c *Client) signedURLWithOptions(now time.Time, nonce uint32, voiceID, hotwords string, extra url.Values) string {
	u, _ := url.Parse(c.cfg.Endpoint) // validated by New
	u.Path = "/asr/v2/" + c.cfg.AppID
	params := url.Values{}
	params.Set("secretid", c.cfg.SecretID)
	params.Set("timestamp", strconv.FormatInt(now.Unix(), 10))
	params.Set("expired", strconv.FormatInt(now.Add(time.Hour).Unix(), 10))
	params.Set("nonce", strconv.FormatUint(uint64(nonce), 10))
	params.Set("engine_model_type", c.cfg.EngineModel)
	// Tencent's official SpeakerRecognizer sends sentence-mode and
	// diarization flags for the speaker-enabled engine. The plain 2.0
	// engine rejects result_mod=1, so never send these flags on other engines.
	if c.cfg.EngineModel == "16k_zh_en_speaker_2.0" {
		params.Set("result_mod", "1")
		params.Set("sentence_strategy", "1")
		params.Set("speaker_diarization", "1")
	}
	params.Set("voice_id", voiceID)
	params.Set("voice_format", "1")
	if c.cfg.HotwordID != "" {
		params.Set("hotword_id", c.cfg.HotwordID)
	}
	if hotwords != "" {
		params.Set("hotword_list", hotwords)
	}
	if c.cfg.InputSampleRate != 0 {
		params.Set("input_sample_rate", strconv.Itoa(c.cfg.InputSampleRate))
	}
	if c.cfg.NeedVAD {
		params.Set("needvad", "1")
	} else {
		params.Set("needvad", "0")
	}
	if c.cfg.VADSilenceTime != 0 {
		params.Set("vad_silence_time", strconv.FormatInt(c.cfg.VADSilenceTime.Milliseconds(), 10))
	}
	if c.cfg.MaxSpeakTime != 0 {
		params.Set("max_speak_time", strconv.FormatInt(c.cfg.MaxSpeakTime.Milliseconds(), 10))
	}
	for key, values := range extra {
		params[key] = values
	}
	// Tencent signs raw parameter values; url.Values.Encode is only for
	// transmission. Signing the encoded UTF-8 bytes rejects Chinese hotwords.
	mac := hmac.New(sha1.New, []byte(c.cfg.SecretKey))
	_, _ = mac.Write([]byte(u.Host + u.Path + "?" + signatureQuery(params)))
	params.Set("signature", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	u.RawQuery = params.Encode()
	return u.String()
}

// signatureQuery implements Tencent's sorted, unescaped key=value canonical
// string. All of the provider's query parameters are single-valued.
func signatureQuery(params url.Values) string {
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+params.Get(key))
	}
	return strings.Join(parts, "&")
}

func argumentError(message string) error {
	return &speecherr.Error{Code: speecherr.CodeInvalidArgument, Provider: "tencent", Operation: "configure", Err: errors.New(message)}
}
func requestError(message string) error {
	return &speecherr.Error{Code: speecherr.CodeInvalidArgument, Provider: "tencent", Operation: "start", Err: errors.New(message)}
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
	code, retryable := errclass.Dial(err, resp)
	message := "WebSocket connection failed"
	if resp != nil {
		message = fmt.Sprintf("WebSocket handshake HTTP %d", resp.StatusCode)
	}
	return &speecherr.Error{
		Code: code, Provider: "tencent", Operation: "connect",
		Err: errors.New(message), Retryable: retryable,
	}
}

func validateSegmentation(cfg Config) error {
	if cfg.VADSilenceTime != 0 {
		if !cfg.NeedVAD {
			return argumentError("VADSilenceTime requires NeedVAD")
		}
		if cfg.VADSilenceTime < 500*time.Millisecond || cfg.VADSilenceTime > 2*time.Second ||
			cfg.VADSilenceTime%time.Millisecond != 0 {
			return argumentError("VADSilenceTime must be a whole number of milliseconds between 500 and 2000")
		}
		switch cfg.EngineModel {
		case "8k_zh", "8k_zh_finance", "16k_zh", "16k_zh_en",
			"8k_zh_large", "16k_en", "16k_en_large":
		default:
			return argumentError("VADSilenceTime is unsupported by EngineModel")
		}
	}
	if cfg.MaxSpeakTime != 0 {
		if cfg.MaxSpeakTime < 5*time.Second || cfg.MaxSpeakTime > 90*time.Second ||
			cfg.MaxSpeakTime%time.Millisecond != 0 {
			return argumentError("MaxSpeakTime must be a whole number of milliseconds between 5000 and 90000")
		}
		switch cfg.EngineModel {
		case "8k_zh", "16k_zh", "16k_zh_en", "8k_zh_large":
		default:
			return argumentError("MaxSpeakTime is unsupported by EngineModel")
		}
	}
	return nil
}
