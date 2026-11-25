package volcengine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/NSObjects/go-speech/tts"
)

const (
	closedReason  = "TTS_STREAM_FINAL_REASON_CLOSED"
	flushedReason = "TTS_STREAM_FINAL_REASON_FLUSHED"
)

type remoteError struct {
	Type  string `json:"type"`
	Code  string `json:"code"`
	LogID string `json:"logid"`
}

type event struct {
	Event     string       `json:"event"`
	SessionID string       `json:"session_id,omitempty"`
	Audio     []byte       `json:"audio,omitempty"`
	IsFinal   bool         `json:"is_final,omitempty"`
	Reason    string       `json:"reason,omitempty"`
	Error     *remoteError `json:"error,omitempty"`
}

func (e event) failure() error {
	if e.Error == nil {
		return protocol("missing provider error")
	}
	code := speecherr.CodeUnavailable
	switch e.Error.Type {
	case "validation_error":
		code = speecherr.CodeInvalidArgument
	case "authentication_error", "authorization_error":
		code = speecherr.CodeUnauthorized
	}
	switch e.Error.Code {
	case "rate_limit_exceeded", "concurrent_limit_exceeded":
		code = speecherr.CodeRateLimited
	}
	// Do not echo arbitrary provider messages containing input text or secrets.
	return &speecherr.Error{Code: code, Provider: "volcengine", Operation: "seed3_receive",
		RemoteCode: e.Error.Code}
}

func decodeEvent(raw []byte) (event, error) {
	var e event
	if len(raw) == 0 || len(raw) > maxEventBytes || json.Unmarshal(raw, &e) != nil || e.Event == "" {
		return e, protocol("invalid JSON event")
	}
	return e, nil
}

// stream is shared by HTTP and WebSocket adapters. The receiver owns terminal
// state; Close is concurrent-safe and unblocks pending reads and writes.
type stream struct {
	parent    context.Context
	format    audio.Format
	next      func() (event, error)
	closeFn   func()
	done      chan struct{}
	once      sync.Once
	mu        sync.Mutex
	ready     bool
	audioSeen bool
	terminal  error
}

func newStream(ctx context.Context, format audio.Format, next func() (event, error), closeFn func()) *stream {
	s := &stream{parent: ctx, format: format, next: next, closeFn: closeFn, done: make(chan struct{})}
	go func() {
		select {
		case <-ctx.Done():
			_ = s.Close()
		case <-s.done:
		}
	}()
	return s
}

func (s *stream) Close() error {
	s.once.Do(func() {
		close(s.done)
		s.closeFn()
	})
	return nil
}

func (s *stream) closed() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

func (s *stream) contextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.parent.Err(); err != nil {
		return err
	}
	if s.closed() {
		return tts.ErrStreamClosed
	}
	return nil
}

func (s *stream) fail(err error) (audio.Chunk, error) {
	s.terminal = err
	_ = s.Close()
	return audio.Chunk{}, err
}

func (s *stream) Recv(ctx context.Context) (audio.Chunk, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminal != nil {
		return audio.Chunk{}, s.terminal
	}
	if err := s.contextError(ctx); err != nil {
		return s.fail(err)
	}
	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
	defer stop()
	for {
		e, err := s.next()
		if ctxErr := s.contextError(ctx); ctxErr != nil {
			return s.fail(ctxErr)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = protocol("stream ended before CLOSED final")
			}
			return s.fail(err)
		}
		switch e.Event {
		case "error":
			return s.fail(e.failure())
		case "ready":
			if s.ready || e.SessionID == "" {
				return s.fail(protocol("invalid ready event"))
			}
			s.ready = true
		case "audio":
			if !s.ready || len(e.Audio) == 0 {
				return s.fail(protocol("invalid audio event"))
			}
			s.audioSeen = true
			return audio.Chunk{Format: s.format, Data: e.Audio}, nil
		case "alignment":
			continue
		case "final":
			if !s.ready || !e.IsFinal {
				return s.fail(protocol("invalid final event"))
			}
			switch e.Reason {
			case flushedReason:
				continue
			case closedReason:
				if !s.audioSeen {
					return s.fail(protocol("no synthesized audio"))
				}
				return s.fail(io.EOF)
			default:
				return s.fail(protocol("unknown final reason"))
			}
		default:
			return s.fail(protocol("unknown event"))
		}
	}
}
