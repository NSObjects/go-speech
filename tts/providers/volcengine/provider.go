// Package volcengine implements Volcengine Seed TTS v3 unidirectional
// streaming over WebSocket. The adapter never transcodes audio.
package volcengine

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/NSObjects/go-speech/tts"
	"github.com/gorilla/websocket"
)

const (
	defaultEndpoint = "wss://openspeech.bytedance.com/api/v3/tts/unidirectional/stream"
	ResourceSeedTTS2 = "seed-tts-2.0"
	defaultTimeout = 15 * time.Second
	maxTextRunes = 20000
	maxBatchAudio = 64 << 20
)

var (
	_ tts.Synthesizer = (*Client)(nil)
	_ tts.StreamSynthesizer = (*Client)(nil)
)

// Config uses the new Doubao Speech console's API Key. ResourceID controls
// model entitlement and billing; the old AppID/AccessToken flow is omitted.
type Config struct {
	APIKey string
	ResourceID string
	Endpoint string
	StartTimeout time.Duration
	Dialer *websocket.Dialer
}

type Client struct{ cfg Config }

func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, invalid("APIKey is required")
	}
	if cfg.ResourceID == "" {
		cfg.ResourceID = ResourceSeedTTS2
	}
	if strings.TrimSpace(cfg.ResourceID) == "" {
		return nil, invalid("ResourceID is required")
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = defaultEndpoint
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" ||
		u.Fragment != "" || u.Path != "/api/v3/tts/unidirectional/stream" ||
		(u.Scheme != "wss" && !(u.Scheme == "ws" && loopback(u.Hostname()))) {
		return nil, invalid("Endpoint must be a secure V3 TTS unidirectional WebSocket URL")
	}
	if cfg.StartTimeout < 0 {
		return nil, invalid("StartTimeout must not be negative")
	}
	if cfg.StartTimeout == 0 {
		cfg.StartTimeout = defaultTimeout
	}
	if cfg.Dialer != nil {
		copy := *cfg.Dialer
		cfg.Dialer = &copy
	}
	return &Client{cfg: cfg}, nil
}

func loopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// SupportedAudio describes the union of V3 unidirectional output formats.
// EncodingOpus denotes an Ogg Opus bitstream, which requires 48 kHz;
// validateRequest enforces this format-specific restriction.
func SupportedAudio() audio.Capability {
	return audio.Capability{
		Encodings: []audio.Encoding{
			audio.EncodingPCM16LE, audio.EncodingMP3, audio.EncodingOpus,
		},
		SampleRates: []int{8000, 16000, 22050, 24000, 32000, 44100, 48000},
		Channels: []int{1},
	}
}

func validateRequest(req tts.Request) (tts.Request, string, error) {
	if !utf8.ValidString(req.Text) || strings.TrimSpace(req.Text) == "" ||
		utf8.RuneCountInString(req.Text) > maxTextRunes {
		return req, "", invalid("Text must contain 1–20000 valid UTF-8 characters")
	}
	if strings.TrimSpace(req.Voice) == "" {
		return req, "", invalid("Voice must be a valid provider speaker ID")
	}
	if req.Format == (audio.Format{}) {
		req.Format = audio.Format{Encoding: audio.EncodingMP3, SampleRate: 24000, Channels: 1}
	}
	if err := req.Format.Validate(); err != nil {
		return req, "", &speecherr.Error{
			Code: speecherr.CodeInvalidArgument, Provider: "volcengine", Operation: "tts", Err: err,
		}
	}
	if err := SupportedAudio().Validate(req.Format); err != nil {
		return req, "", &speecherr.Error{
			Code: speecherr.CodeUnsupported, Provider: "volcengine", Operation: "tts", Err: err,
		}
	}
	switch req.Format.Encoding {
	case audio.EncodingPCM16LE:
		return req, "pcm", nil
	case audio.EncodingMP3:
		return req, "mp3", nil
	case audio.EncodingOpus:
		// The V3 unidirectional TTS endpoint only emits Ogg Opus at 48 kHz.
		// Reject other sample rates before opening a billable session.
		if req.Format.SampleRate != 48000 {
			return req, "", &speecherr.Error{
				Code: speecherr.CodeUnsupported, Provider: "volcengine", Operation: "tts",
				Err: fmt.Errorf("Ogg Opus TTS requires 48000 Hz; got %d", req.Format.SampleRate),
			}
		}
		return req, "ogg_opus", nil
	default:
		return req, "", invalid("unsupported audio encoding")
	}
}

func newID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	id[6] = id[6]&15 | 0x40
	id[8] = id[8]&63 | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:]), nil
}

func (c *Client) SynthesizeStream(ctx context.Context, req tts.Request) (tts.Stream, error) {
	if c == nil {
		return nil, invalid("nil client")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	req, encoding, err := validateRequest(req)
	if err != nil {
		return nil, err
	}
	id, err := newID()
	if err != nil {
		return nil, &speecherr.Error{Code: speecherr.CodeInternal, Provider: "volcengine", Operation: "id", Err: err}
	}
	headers := http.Header{}
	headers.Set("X-Api-Key", c.cfg.APIKey)
	headers.Set("X-Api-Resource-Id", c.cfg.ResourceID)
	headers.Set("X-Api-Request-Id", id)
	dialer := *websocket.DefaultDialer
	if c.cfg.Dialer != nil {
		dialer = *c.cfg.Dialer
	}
	if dialer.HandshakeTimeout == 0 {
		dialer.HandshakeTimeout = c.cfg.StartTimeout
	}
	conn, resp, err := dialer.DialContext(ctx, c.cfg.Endpoint, headers)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, dialFailure(err, resp)
	}
	conn.SetReadLimit(maxWirePayload + 256)
	stream := newStream(conn, ctx, req.Format)
	if err := stream.sendText(ctx, req, encoding); err != nil {
		_ = stream.Close()
		return nil, err
	}
	return stream, nil
}

func (c *Client) Synthesize(ctx context.Context, req tts.Request) (audio.Buffer, error) {
	s, err := c.SynthesizeStream(ctx, req)
	if err != nil {
		return audio.Buffer{}, err
	}
	defer s.Close()
	var out audio.Buffer
	for {
		chunk, err := s.Recv(ctx)
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
			return audio.Buffer{}, &speecherr.Error{
				Code: speecherr.CodeInternal, Provider: "volcengine", Operation: "tts",
				Err: errors.New("batch audio exceeds 64 MiB; use SynthesizeStream"),
			}
		}
		if out.Format == (audio.Format{}) {
			out.Format = chunk.Format
		}
		out.Data = append(out.Data, chunk.Data...)
	}
}

func invalid(msg string) error {
	return &speecherr.Error{
		Code: speecherr.CodeInvalidArgument, Provider: "volcengine", Operation: "tts", Err: errors.New(msg),
	}
}
func protocol(msg string) error {
	return &speecherr.Error{
		Code: speecherr.CodeProtocol, Provider: "volcengine", Operation: "tts", Err: errors.New(msg),
	}
}
func dialFailure(err error, resp *http.Response) error {
	code := speecherr.CodeUnavailable
	if resp != nil {
		switch resp.StatusCode {
		case http.StatusBadRequest:
			code = speecherr.CodeInvalidArgument
		case http.StatusUnauthorized, http.StatusForbidden:
			code = speecherr.CodeUnauthorized
		case http.StatusTooManyRequests:
			code = speecherr.CodeRateLimited
		}
		err = fmt.Errorf("WebSocket handshake HTTP %d: %w", resp.StatusCode, err)
	}
	return &speecherr.Error{
		Code: code, Provider: "volcengine", Operation: "connect", Err: err,
		Retryable: code == speecherr.CodeUnavailable || code == speecherr.CodeRateLimited,
	}
}
