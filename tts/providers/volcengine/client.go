// Package volcengine implements Volcengine Seed-TTS 3.0 using HTTP JSONL and
// WebSocket JSON events. It does not train voices or transcode audio.
package volcengine

import (
	"context"
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
	defaultEndpoint = "https://voice.cn-beijing.volces.com"
	defaultModel    = "doubao-seed-tts-3.0"
	maxTextRunes    = 10000
	maxEventBytes   = 8 << 20
	maxBatchBytes   = 64 << 20
)

// Config selects the Seed-TTS 3.0 model and API key. Endpoint is the optional
// HTTPS origin (not a complete request path). LanguageCode defaults to auto.
type Config struct {
	APIKey       string
	ModelID      string
	LanguageCode string
	Endpoint     string
	StartTimeout time.Duration
	Dialer       *websocket.Dialer
}

// Client implements the shared TTS interfaces without a persistent connection.
type Client struct {
	cfg  Config
	base *url.URL
	http *http.Client
}

var (
	_ tts.Client            = (*Client)(nil)
	_ tts.DuplexSynthesizer = (*Client)(nil)
)

// New validates configuration without opening a network connection.
func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, invalid("APIKey is required")
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = defaultEndpoint
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Path != "" && u.Path != "/") ||
		(u.Scheme != "https" && !(u.Scheme == "http" && loopback(u.Hostname()))) {
		return nil, invalid("Endpoint must be an HTTPS origin")
	}
	if cfg.StartTimeout < 0 {
		return nil, invalid("negative StartTimeout")
	}
	if cfg.StartTimeout == 0 {
		cfg.StartTimeout = 15 * time.Second
	}
	if cfg.ModelID == "" {
		cfg.ModelID = defaultModel
	}
	if cfg.LanguageCode == "" {
		cfg.LanguageCode = "auto"
	}
	if cfg.Dialer != nil {
		dialer := *cfg.Dialer
		cfg.Dialer = &dialer
	}
	return &Client{cfg: cfg, base: u, http: &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func loopback(host string) bool {
	ip := net.ParseIP(host)
	return strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
}

func failure(code speecherr.Code, op string, err error) error {
	return &speecherr.Error{Code: code, Provider: "volcengine", Operation: "seed3_" + op, Err: err}
}

func invalid(message string) error {
	return failure(speecherr.CodeInvalidArgument, "validate", errors.New(message))
}

func protocol(message string) error {
	return failure(speecherr.CodeProtocol, "receive", errors.New(message))
}

func validText(text string) bool {
	return utf8.ValidString(text) && strings.TrimSpace(text) != "" && utf8.RuneCountInString(text) <= maxTextRunes
}

type outputFormat struct {
	Format     string `json:"format"`
	SampleRate int    `json:"sample_rate"`
	BitRate    int    `json:"bit_rate,omitempty"`
}

// SupportedAudio describes mono MP3, PCM16LE and Ogg Opus. Ogg Opus is
// restricted to 48000 Hz by validateOutput.
func SupportedAudio() audio.Capability {
	return audio.Capability{
		Encodings:   []audio.Encoding{audio.EncodingMP3, audio.EncodingPCM16LE, audio.EncodingOpus},
		SampleRates: []int{8000, 16000, 22050, 24000, 32000, 44100, 48000},
		Channels:    []int{1},
	}
}

func validateOutput(voice string, f audio.Format) (audio.Format, outputFormat, error) {
	if strings.TrimSpace(voice) == "" || voice != strings.TrimSpace(voice) || len(voice) > 512 ||
		strings.ContainsAny(voice, "/\\\r\n") || voice == "." || voice == ".." {
		return f, outputFormat{}, invalid("invalid voice ID")
	}
	if f == (audio.Format{}) {
		f = audio.Format{Encoding: audio.EncodingMP3, SampleRate: 24000, Channels: 1}
	}
	if err := SupportedAudio().Validate(f); err != nil {
		return f, outputFormat{}, failure(speecherr.CodeUnsupported, "validate", err)
	}
	out := outputFormat{SampleRate: f.SampleRate}
	switch f.Encoding {
	case audio.EncodingMP3:
		out.Format, out.BitRate = "mp3", 64000
	case audio.EncodingPCM16LE:
		out.Format = "pcm"
	case audio.EncodingOpus:
		if f.SampleRate != 48000 {
			return f, out, invalid("Ogg Opus requires 48000 Hz")
		}
		out.Format, out.BitRate = "ogg_opus", 64000
	}
	return f, out, nil
}

func (c *Client) endpoint(voice, operation string, socket bool) string {
	u := *c.base
	u.Path = "/api/v3/text-to-speech/" + voice + "/" + operation
	u.RawPath = "/api/v3/text-to-speech/" + url.PathEscape(voice) + "/" + operation
	if socket {
		if u.Scheme == "https" {
			u.Scheme = "wss"
		} else {
			u.Scheme = "ws"
		}
	}
	return u.String()
}

// Synthesize collects one complete bounded bitstream from the same HTTP
// streaming implementation used by SynthesizeStream.
func (c *Client) Synthesize(ctx context.Context, req tts.Request) (audio.Buffer, error) {
	if err := c.validateBatchOptions(req); err != nil {
		return audio.Buffer{}, err
	}
	stream, err := c.SynthesizeStream(ctx, req)
	if err != nil {
		return audio.Buffer{}, err
	}
	defer func() { _ = stream.Close() }()
	var result audio.Buffer
	for {
		chunk, err := stream.Recv(ctx)
		if errors.Is(err, io.EOF) {
			return result, nil
		}
		if err != nil {
			return audio.Buffer{}, err
		}
		if len(result.Data)+len(chunk.Data) > maxBatchBytes {
			return audio.Buffer{}, invalid("synthesized audio exceeds batch limit")
		}
		result.Format = chunk.Format
		result.Data = append(result.Data, chunk.Data...)
	}
}

func httpFailure(status int) error {
	code := speecherr.CodeUnavailable
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		code = speecherr.CodeUnauthorized
	case http.StatusTooManyRequests:
		code = speecherr.CodeRateLimited
	case http.StatusBadRequest, http.StatusNotFound:
		code = speecherr.CodeInvalidArgument
	}
	return failure(code, "request", fmt.Errorf("HTTP %d", status))
}
