// Package aliyun implements Alibaba Cloud CosyVoice TTS over the DashScope
// WebSocket task protocol. Each synthesis owns its own WebSocket connection.
// Audio bytes are passed through unchanged; no resampling or transcoding.
package aliyun

import (
	"context"
	"crypto/rand"
	"encoding/json"
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

const DefaultEndpoint = "wss://dashscope.aliyuncs.com/api-ws/v1/inference"

const (
	ModelCosyVoiceV3Flash = "cosyvoice-v3-flash"
	defaultStartTimeout   = 15 * time.Second
	defaultWriteTimeout   = 10 * time.Second
	maxTextRunes          = 20000
	maxBatchAudio         = 64 << 20
)

var (
	_ tts.Synthesizer       = (*Client)(nil)
	_ tts.StreamSynthesizer = (*Client)(nil)
	_ tts.DuplexSynthesizer = (*Client)(nil)
)

// Config holds credentials and transport settings. Endpoint defaults to the
// China DashScope URL; override it for another region or workspace-specific URL.
// Models and voice identifiers are supplied by Alibaba Cloud, not remapped.
type Config struct {
	APIKey       string
	Endpoint     string
	Model        string
	WorkspaceID  string
	StartTimeout time.Duration
	Dialer       *websocket.Dialer
}

type Client struct{ cfg Config }

func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, invalid("APIKey is required")
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = DefaultEndpoint
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		u.Path != "/api-ws/v1/inference" {
		return nil, invalid("Endpoint must be a WebSocket /api-ws/v1/inference URL")
	}
	if u.Scheme != "wss" && !(u.Scheme == "ws" && loopback(u.Hostname())) {
		return nil, invalid("wss is required except for local mock servers")
	}
	if cfg.StartTimeout < 0 {
		return nil, invalid("StartTimeout cannot be negative")
	}
	if cfg.StartTimeout == 0 {
		cfg.StartTimeout = defaultStartTimeout
	}
	if cfg.Model == "" {
		cfg.Model = ModelCosyVoiceV3Flash
	}
	if cfg.Dialer != nil {
		copied := *cfg.Dialer
		cfg.Dialer = &copied
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

// SupportedAudio describes CosyVoice WebSocket output formats. EncodingOpus
// denotes Ogg-encapsulated Opus, never raw codec packets. Audio is delivered
// unchanged; transport chunks need not align with Ogg page boundaries.
func SupportedAudio() audio.Capability {
	return audio.Capability{
		Encodings: []audio.Encoding{
			audio.EncodingPCM16LE, audio.EncodingWAV, audio.EncodingMP3, audio.EncodingOpus,
		},
		SampleRates: []int{8000, 16000, 22050, 24000, 44100, 48000},
		Channels:    []int{1},
	}
}

func validateRequest(req tts.Request) (tts.Request, string, error) {
	if strings.TrimSpace(req.Text) == "" {
		return req, "", invalid("Text cannot be empty")
	}
	if !utf8.ValidString(req.Text) || utf8.RuneCountInString(req.Text) > maxTextRunes {
		return req, "", invalid("Text must be valid UTF-8 and at most 20000 Unicode characters")
	}
	if strings.TrimSpace(req.Voice) == "" {
		return req, "", invalid("Voice is required and must match the selected model")
	}
	if req.Format == (audio.Format{}) {
		req.Format = audio.Format{Encoding: audio.EncodingMP3, SampleRate: 22050, Channels: 1}
	}
	if err := req.Format.Validate(); err != nil {
		return req, "", &speecherr.Error{Code: speecherr.CodeInvalidArgument, Provider: "aliyun", Operation: "tts", Err: err}
	}
	if err := SupportedAudio().Validate(req.Format); err != nil {
		return req, "", &speecherr.Error{Code: speecherr.CodeUnsupported, Provider: "aliyun", Operation: "tts", Err: err}
	}
	switch req.Format.Encoding {
	case audio.EncodingPCM16LE:
		return req, "pcm", nil
	case audio.EncodingWAV:
		return req, "wav", nil
	case audio.EncodingMP3:
		return req, "mp3", nil
	case audio.EncodingOpus:
		return req, "opus", nil
	default:
		return req, "", unsupported("unsupported audio encoding")
	}
}

// SynthesizeStream sends one complete text request, signals finish-task and
// returns a stream of the provider's binary audio frames. The session stays
// alive until task-finished, a terminal error, or cancellation.
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
	taskID, err := taskID()
	if err != nil {
		return nil, &speecherr.Error{Code: speecherr.CodeInternal, Provider: "aliyun", Operation: "task_id", Err: err}
	}
	dialer := *websocket.DefaultDialer
	if c.cfg.Dialer != nil {
		dialer = *c.cfg.Dialer
	}
	if dialer.HandshakeTimeout == 0 {
		dialer.HandshakeTimeout = c.cfg.StartTimeout
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+c.cfg.APIKey)
	if c.cfg.WorkspaceID != "" {
		headers.Set("X-DashScope-WorkSpace", c.cfg.WorkspaceID)
	}
	conn, response, err := dialer.DialContext(ctx, c.cfg.Endpoint, headers)
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
	stream := newStream(conn, ctx, taskID, req.Format)
	if err := stream.start(ctx, c.cfg.Model, req, encoding, c.cfg.StartTimeout); err != nil {
		_ = stream.Close()
		return nil, err
	}
	return stream, nil
}

// OpenDuplex opens a CosyVoice session for incremental text input.
func (c *Client) OpenDuplex(ctx context.Context, cfg tts.DuplexRequest) (tts.DuplexStream, error) {
	if c == nil {
		return nil, invalid("nil client")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	req, encoding, err := validateRequest(tts.Request{Text: "validation", Voice: cfg.Voice, Format: cfg.Format})
	if err != nil {
		return nil, err
	}
	taskID, err := taskID()
	if err != nil {
		return nil, &speecherr.Error{Code: speecherr.CodeInternal, Provider: "aliyun", Operation: "task_id", Err: err}
	}
	dialer := *websocket.DefaultDialer
	if c.cfg.Dialer != nil {
		dialer = *c.cfg.Dialer
	}
	if dialer.HandshakeTimeout == 0 {
		dialer.HandshakeTimeout = c.cfg.StartTimeout
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+c.cfg.APIKey)
	if c.cfg.WorkspaceID != "" {
		headers.Set("X-DashScope-WorkSpace", c.cfg.WorkspaceID)
	}
	conn, response, err := dialer.DialContext(ctx, c.cfg.Endpoint, headers)
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
	stream := newStream(conn, ctx, taskID, req.Format)
	if err := stream.startSession(ctx, c.cfg.Model, req, encoding, c.cfg.StartTimeout); err != nil {
		_ = stream.Close()
		return nil, err
	}
	return stream, nil
}

// Synthesize collects the same streaming synthesis into a complete buffer.
// The 64 MiB cap prevents accidental unbounded memory usage by batch callers.
func (c *Client) Synthesize(ctx context.Context, req tts.Request) (audio.Buffer, error) {
	s, err := c.SynthesizeStream(ctx, req)
	if err != nil {
		return audio.Buffer{}, err
	}
	defer s.Close()
	var output audio.Buffer
	for {
		chunk, err := s.Recv(ctx)
		if errors.Is(err, io.EOF) {
			if len(output.Data) == 0 {
				return audio.Buffer{}, protocol("synthesis completed without audio")
			}
			return output, nil
		}
		if err != nil {
			return audio.Buffer{}, err
		}
		if len(chunk.Data) > maxBatchAudio-len(output.Data) {
			return audio.Buffer{}, &speecherr.Error{
				Code: speecherr.CodeInternal, Provider: "aliyun", Operation: "tts",
				Err: errors.New("batch audio exceeds 64 MiB; use SynthesizeStream"),
			}
		}
		if output.Format == (audio.Format{}) {
			output.Format = chunk.Format
		}
		output.Data = append(output.Data, chunk.Data...)
	}
}

func taskID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	id[6] = id[6]&15 | 0x40
	id[8] = id[8]&63 | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:]), nil
}

func invalid(msg string) error {
	return &speecherr.Error{Code: speecherr.CodeInvalidArgument, Provider: "aliyun", Operation: "tts", Err: errors.New(msg)}
}
func unsupported(msg string) error {
	return &speecherr.Error{Code: speecherr.CodeUnsupported, Provider: "aliyun", Operation: "tts", Err: errors.New(msg)}
}
func protocol(msg string) error {
	return &speecherr.Error{Code: speecherr.CodeProtocol, Provider: "aliyun", Operation: "tts", Err: errors.New(msg)}
}
func dialFailure(err error, resp *http.Response) error {
	code := speecherr.CodeUnavailable
	if resp != nil {
		switch resp.StatusCode {
		case 400:
			code = speecherr.CodeInvalidArgument
		case 401, 403:
			code = speecherr.CodeUnauthorized
		case 429:
			code = speecherr.CodeRateLimited
		}
		// Only the status code is included; never echo credentials, headers
		// or the untrusted remote response body.
		err = fmt.Errorf("WebSocket handshake HTTP %d: %w", resp.StatusCode, err)
	}
	return &speecherr.Error{
		Code: code, Provider: "aliyun", Operation: "connect", Err: err,
		Retryable: code == speecherr.CodeUnavailable || code == speecherr.CodeRateLimited,
	}
}

func classifyTaskFailure(header serverHeader) error {
	// Prefer the provider's machine-readable code. Human-readable messages
	// are not stable and must not make exhausted quota look retryable.
	remote := strings.ToLower(strings.TrimSpace(header.ErrorCode))
	code := speecherr.CodeInternal
	retryable := false
	switch {
	case strings.Contains(remote, "invalidapikey"), strings.Contains(remote, "accessdenied"),
		strings.Contains(remote, "unauthorized"), strings.Contains(remote, "permission"),
		strings.Contains(remote, "authentication"):
		code = speecherr.CodeUnauthorized
	case strings.Contains(remote, "quota"), strings.Contains(remote, "allocation"),
		strings.Contains(remote, "billing"), strings.Contains(remote, "balance"):
		code = speecherr.CodeRateLimited
	case strings.Contains(remote, "throttl"), strings.Contains(remote, "ratelimit"),
		strings.Contains(remote, "too_many_requests"):
		code, retryable = speecherr.CodeRateLimited, true
	case strings.Contains(remote, "timeout"):
		code, retryable = speecherr.CodeTimeout, true
	case strings.Contains(remote, "unavailable"), strings.Contains(remote, "servererror"):
		code, retryable = speecherr.CodeUnavailable, true
	case strings.Contains(remote, "invalidparameter"), strings.Contains(remote, "modelnotfound"):
		code = speecherr.CodeInvalidArgument
	}
	return &speecherr.Error{
		Code: code, Provider: "aliyun", Operation: "task", RemoteCode: header.ErrorCode,
		Retryable: retryable,
		Err:       fmt.Errorf("remote task %s: %s", header.ErrorCode, header.ErrorMessage),
	}
}

// Event payload for control messages; binary messages carry raw audio.
type serverHeader struct {
	Event        string `json:"event"`
	TaskID       string `json:"task_id"`
	ErrorCode    string `json:"error_code"`
	ErrorMessage string `json:"error_message"`
}
type serverEvent struct {
	Header serverHeader `json:"header"`
}

func decodeEvent(data []byte, task string) (serverHeader, error) {
	var event serverEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return serverHeader{}, &speecherr.Error{
			Code: speecherr.CodeProtocol, Provider: "aliyun", Operation: "decode", Err: err,
		}
	}
	if event.Header.Event == "" || event.Header.TaskID != task {
		return serverHeader{}, protocol("missing event or mismatched task_id")
	}
	return event.Header, nil
}
