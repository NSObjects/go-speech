// Package volcengine implements Doubao large-model, bidirectional streaming
// ASR over the official Volcengine WebSocket binary protocol. Audio is passed
// through unchanged; the caller must supply a model-compatible format.
package volcengine

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/gorilla/websocket"
)

const (
	DefaultEndpoint            = "wss://openspeech.bytedance.com/api/v3/sauc/bigmodel_async"
	ResourceSeedASR2           = "volc.seedasr.sauc.duration"
	ResourceSeedASR2Concurrent = "volc.seedasr.sauc.concurrent"
	defaultStartTimeout        = 15 * time.Second

	// ResultFull returns cumulative transcript snapshots from the provider.
	ResultFull = "full"
	// ResultSingle returns only the current utterance in each result.
	ResultSingle = "single"
)

var _ asr.StreamRecognizer = (*Client)(nil)

// Config specifies the Volcengine streaming ASR transport and credentials.
// Use an X-Api-Key from the speech model console, not an Alibaba API key.
// ResourceID selects the enabled ASR service and billing mode.
// Each OpenStream creates its own WebSocket; no global mutable state.
type Config struct {
	APIKey          string
	ResourceID      string
	Endpoint        string
	UID             string
	StartTimeout    time.Duration
	Dialer          *websocket.Dialer
	EnableNonStream bool          // Optional second-pass recognition for stable endings.
	ResultType      string        // "full" (default) or "single"; controls provider response mode.
	EndWindowSize   time.Duration // Optional provider-side silence window; 0 uses the service default (~800ms).
}

type Client struct{ cfg Config }

func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, argumentError("APIKey is required")
	}
	if cfg.ResultType == "" {
		cfg.ResultType = ResultFull
	}
	if cfg.ResultType != ResultFull && cfg.ResultType != ResultSingle {
		return nil, argumentError("ResultType must be full or single")
	}
	if cfg.EndWindowSize != 0 && (cfg.EndWindowSize < 200*time.Millisecond || cfg.EndWindowSize%time.Millisecond != 0) {
		return nil, argumentError("EndWindowSize must be at least 200ms and a whole number of milliseconds")
	}
	if strings.TrimSpace(cfg.ResourceID) == "" {
		cfg.ResourceID = ResourceSeedASR2
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = DefaultEndpoint
	}
	if cfg.StartTimeout < 0 {
		return nil, argumentError("StartTimeout cannot be negative")
	}
	if cfg.StartTimeout == 0 {
		cfg.StartTimeout = defaultStartTimeout
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, argumentError("invalid Volcengine WebSocket endpoint")
	}
	if u.Scheme != "wss" && !(u.Scheme == "ws" && isLoopback(u.Hostname())) {
		return nil, argumentError("wss is required outside loopback tests")
	}
	if u.Path != "/api/v3/sauc/bigmodel_async" {
		return nil, argumentError("only the bidirectional optimized bigmodel_async endpoint is supported")
	}
	if cfg.UID == "" {
		cfg.UID = "go-speech"
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

func (c *Client) OpenStream(ctx context.Context, request asr.Config) (asr.Stream, error) {
	if c == nil {
		return nil, argumentError("nil client")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	format, codec, err := modelAudio(request.Format)
	if err != nil {
		return nil, err
	}
	if request.Language != "" {
		return nil, unsupportedError("Language selection is not supported on bigmodel_async")
	}
	req := map[string]any{
		"user": map[string]any{"uid": c.cfg.UID},
		"audio": map[string]any{
			"format": format, "codec": codec, "rate": request.Format.SampleRate,
			"bits": 16, "channel": request.Format.Channels,
		},
		"request": map[string]any{
			"model_name": "bigmodel", "enable_itn": true, "enable_punc": true,
			"show_utterances": true, "enable_nonstream": c.cfg.EnableNonStream,
			"result_type": c.cfg.ResultType,
		},
	}
	resultType, details, err := c.cfg.applyOptions(request.Options, req["request"].(map[string]any))
	if err != nil {
		return nil, err
	}
	if err := applyCorpus(request, req["request"].(map[string]any)); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, &speecherr.Error{Code: speecherr.CodeInternal, Provider: "volcengine", Operation: "encode", Err: err}
	}
	frame, err := encodeFrame(messageFullRequest, 0, 1, payload)
	if err != nil {
		return nil, err
	}
	id, err := newRequestID()
	if err != nil {
		return nil, &speecherr.Error{Code: speecherr.CodeInternal, Provider: "volcengine", Operation: "request_id", Err: err}
	}
	dialer := *websocket.DefaultDialer
	if c.cfg.Dialer != nil {
		dialer = *c.cfg.Dialer
	}
	if dialer.HandshakeTimeout == 0 {
		dialer.HandshakeTimeout = c.cfg.StartTimeout
	}
	headers := http.Header{}
	headers.Set("X-Api-Key", c.cfg.APIKey)
	headers.Set("X-Api-Resource-Id", c.cfg.ResourceID)
	headers.Set("X-Api-Request-Id", id)
	headers.Set("X-Api-Connect-Id", id)
	headers.Set("X-Api-Sequence", "-1")
	conn, resp, err := dialer.DialContext(ctx, c.cfg.Endpoint, headers)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		return nil, connectError(err, resp)
	}
	conn.SetReadLimit(maxWirePayload + 128)
	stream := newStream(conn, ctx, resultType)
	stream.details = details
	if request.Format.Encoding == audio.EncodingPCM16LE {
		stream.pcmFrameBytes = request.Format.Channels * 2 // signed 16-bit samples per channel
	}
	if err := stream.writeFrame(ctx, frame); err != nil {
		result := stream.transportError(ctx, "start", err)
		_ = stream.Close()
		return nil, result
	}
	return stream, nil
}

// SupportedAudio reports the accepted input formats for the optimized
// bigmodel_async streaming endpoint. These are transport capabilities: Opus
// requires Ogg encapsulation and audio bytes are never transcoded implicitly.
func SupportedAudio() audio.Capability {
	return audio.Capability{
		Encodings: []audio.Encoding{
			audio.EncodingPCM16LE,
			audio.EncodingWAV,
			audio.EncodingMP3,
			audio.EncodingOpus,
		},
		SampleRates: []int{16000},
		Channels:    []int{1, 2},
	}
}

func modelAudio(f audio.Format) (format, codec string, err error) {
	if err := f.Validate(); err != nil {
		return "", "", &speecherr.Error{Code: speecherr.CodeInvalidArgument, Provider: "volcengine", Operation: "start", Err: err}
	}
	if err := SupportedAudio().Validate(f); err != nil {
		return "", "", &speecherr.Error{
			Code: speecherr.CodeUnsupported, Provider: "volcengine",
			Operation: "start", Err: err,
		}
	}
	switch f.Encoding {
	case audio.EncodingPCM16LE:
		return "pcm", "raw", nil
	case audio.EncodingWAV:
		return "wav", "raw", nil
	case audio.EncodingMP3:
		return "mp3", "raw", nil
	case audio.EncodingOpus:
		return "ogg", "opus", nil // Ogg Opus required; raw Opus is not accepted.
	default:
		return "", "", unsupportedError(fmt.Sprintf("unsupported encoding %q", f.Encoding))
	}
}

func argumentError(msg string) error {
	return &speecherr.Error{Code: speecherr.CodeInvalidArgument, Provider: "volcengine", Operation: "configure", Err: errors.New(msg)}
}

func unsupportedError(msg string) error {
	return &speecherr.Error{Code: speecherr.CodeUnsupported, Provider: "volcengine", Operation: "start", Err: errors.New(msg)}
}

func connectError(err error, resp *http.Response) error {
	code := speecherr.CodeUnavailable
	remoteCode := ""
	if resp != nil {
		switch resp.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			code = speecherr.CodeUnauthorized
		case http.StatusTooManyRequests:
			code = speecherr.CodeRateLimited
		case http.StatusBadRequest:
			code = speecherr.CodeInvalidArgument
		}
		// Report only non-secret protocol metadata. Never print request
		// headers, API keys, response body or untrusted error-message text.
		remoteCode = resp.Header.Get("X-Api-Status-Code")
		logID := resp.Header.Get("X-Tt-Logid")
		err = fmt.Errorf("%w: HTTP %d, X-Api-Status-Code=%q, X-Tt-Logid=%q",
			err, resp.StatusCode, remoteCode, logID)
	} else {
		// The WebSocket handshake can time out before an HTTP response
		// arrives. Expose a typed timeout, not a generic unavailable error.
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			code = speecherr.CodeTimeout
		}
	}
	return &speecherr.Error{
		Code: code, Provider: "volcengine", Operation: "connect",
		RemoteCode: remoteCode, Err: err,
		Retryable: code == speecherr.CodeUnavailable || code == speecherr.CodeRateLimited || code == speecherr.CodeTimeout,
	}
}

func newRequestID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	buf[6] = buf[6]&15 | 0x40
	buf[8] = buf[8]&63 | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[:4], buf[4:6], buf[6:8], buf[8:10], buf[10:]), nil
}
