// Package aliyun implements several Alibaba Cloud realtime ASR models using
// the DashScope WebSocket task protocol. It does not depend on an Alibaba SDK.
package aliyun

import (
	"context"
	"crypto/rand"
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

const DefaultEndpoint = "wss://dashscope.aliyuncs.com/api-ws/v1/inference"

const (
	defaultStartTimeout = 15 * time.Second
	defaultWriteTimeout = 10 * time.Second
)

var _ asr.StreamRecognizer = (*Client)(nil)

// Config provides Alibaba-specific settings. Endpoint is optional and defaults
// to the China DashScope WebSocket URL. Override it for other regions or
// workspace-specific endpoints. Each OpenStream creates a WebSocket connection.
type Config struct {
	Endpoint             string
	APIKey               string
	Model                string
	WorkspaceID          string
	VocabularyID         string
	Vocabulary           map[string]int
	MaxSentenceSilenceMs int
	Heartbeat            bool
	IntermediateResults  bool
	DisfluencyRemoval    bool
	KeepDialect          bool
	VADModel             string
	StartTimeout         time.Duration
	Dialer               *websocket.Dialer
}

type Client struct{ cfg Config }

func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, badArgument("APIKey is required")
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = DefaultEndpoint
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, badArgument("invalid WebSocket endpoint")
	}
	if u.Scheme != "wss" && !(u.Scheme == "ws" && isLoopbackHost(u.Hostname())) {
		return nil, badArgument("use wss:// except for loopback tests")
	}
	if u.Path != "/api-ws/v1/inference" {
		return nil, badArgument("endpoint path must be /api-ws/v1/inference")
	}
	if cfg.StartTimeout < 0 {
		return nil, badArgument("negative StartTimeout")
	}
	if cfg.StartTimeout == 0 {
		cfg.StartTimeout = defaultStartTimeout
	}
	if cfg.Model == "" {
		cfg.Model = ModelQwenAudio31Streaming
	}
	if _, err := modelType(cfg.Model); err != nil {
		return nil, err
	}
	if cfg.Dialer != nil {
		d := *cfg.Dialer
		cfg.Dialer = &d
	}
	if cfg.Vocabulary != nil {
		vocab := make(map[string]int, len(cfg.Vocabulary))
		for word, weight := range cfg.Vocabulary {
			vocab[word] = weight
		}
		cfg.Vocabulary = vocab
	}
	return &Client{cfg: cfg}, nil
}
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
func badArgument(msg string) error {
	return &speecherr.Error{Code: speecherr.CodeInvalidArgument, Provider: "aliyun", Operation: "configure", Err: errors.New(msg)}
}

func (c *Client) OpenStream(ctx context.Context, cfg asr.Config) (asr.Stream, error) {
	if c == nil {
		return nil, badArgument("nil client")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cfg.Options != (asr.Options{}) {
		return nil, &speecherr.Error{Code: speecherr.CodeUnsupported, Provider: "aliyun", Operation: "start", Err: errors.New("advanced ASR options are not yet supported")}
	}
	format, err := mapAudioFormat(cfg.Format)
	if err != nil {
		return nil, err
	}
	params, err := c.cfg.parameters(cfg, format)
	if err != nil {
		return nil, err
	}
	taskID, err := newTaskID()
	if err != nil {
		return nil, &speecherr.Error{Code: speecherr.CodeInternal, Provider: "aliyun", Operation: "task_id", Err: err}
	}
	d := websocket.DefaultDialer
	if c.cfg.Dialer != nil {
		d = c.cfg.Dialer
	}
	dialer := *d
	if dialer.HandshakeTimeout == 0 {
		dialer.HandshakeTimeout = c.cfg.StartTimeout
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	if c.cfg.WorkspaceID != "" {
		header.Set("X-DashScope-WorkSpace", c.cfg.WorkspaceID)
	}
	conn, resp, err := dialer.DialContext(ctx, c.cfg.Endpoint, header)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, dialError(err, resp)
	}
	s := newStream(conn, ctx, taskID)
	if cfg.Format.Encoding == audio.EncodingPCM16LE {
		s.pcmFrameBytes = cfg.Format.Channels * 2 // signed 16-bit samples per channel
	}
	cmd := taskMessage{
		Header:  commandHeader{Action: "run-task", TaskID: taskID, Streaming: "duplex"},
		Payload: taskPayload{TaskGroup: "audio", Task: "asr", Function: "recognition", Model: c.cfg.Model, Parameters: params, Input: map[string]any{}},
	}
	if err = s.writeJSON(ctx, cmd); err != nil {
		_ = s.Close()
		return nil, wrapNetwork(ctx, "run-task", err)
	}
	deadline := time.Now().Add(c.cfg.StartTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err = conn.SetReadDeadline(deadline); err != nil {
		_ = s.Close()
		return nil, wrapNetwork(ctx, "task-started", err)
	}
	err = s.awaitStarted(ctx)
	if err == nil {
		err = conn.SetReadDeadline(time.Time{})
	}
	if err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

func mapAudioFormat(f audio.Format) (string, error) {
	if err := f.Validate(); err != nil {
		return "", &speecherr.Error{Code: speecherr.CodeInvalidArgument, Provider: "aliyun", Operation: "start", Err: err}
	}
	if f.Channels != 1 {
		return "", unsupportedAudio(fmt.Errorf("%w: mono audio required", audio.ErrUnsupportedFormat))
	}
	switch f.Encoding {
	case audio.EncodingPCM16LE:
		return "pcm", nil
	case audio.EncodingWAV:
		return "wav", nil
	case audio.EncodingMP3:
		return "mp3", nil
	case audio.EncodingOpus:
		return "opus", nil // Ogg encapsulation required.
	case audio.EncodingSpeex:
		return "speex", nil // Ogg encapsulation required.
	case audio.EncodingAAC:
		return "aac", nil
	case audio.EncodingAMR:
		return "amr", nil // AMR-NB only.
	default:
		return "", unsupportedAudio(fmt.Errorf("%w: encoding %q", audio.ErrUnsupportedFormat, f.Encoding))
	}
}
func dialError(err error, resp *http.Response) error {
	code := speecherr.CodeUnavailable
	if resp != nil {
		switch resp.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			code = speecherr.CodeUnauthorized
		case http.StatusTooManyRequests:
			code = speecherr.CodeRateLimited
		case http.StatusBadRequest:
			code = speecherr.CodeInvalidArgument
		}
	}
	if resp == nil {
		// A WebSocket dial may return an i/o timeout without wrapping
		// context.DeadlineExceeded. Preserve that distinction for callers.
		var network net.Error
		if errors.Is(err, context.DeadlineExceeded) ||
			(errors.As(err, &network) && network.Timeout()) {
			code = speecherr.CodeTimeout
		}
	}
	return &speecherr.Error{Code: code, Provider: "aliyun", Operation: "connect", Err: err, Retryable: code == speecherr.CodeUnavailable || code == speecherr.CodeRateLimited || code == speecherr.CodeTimeout}
}
func wrapNetwork(ctx context.Context, op string, err error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	code := speecherr.CodeUnavailable
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		code = speecherr.CodeTimeout
	}
	return &speecherr.Error{Code: code, Provider: "aliyun", Operation: op, Err: err, Retryable: true}
}
func newTaskID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:]), nil
}
