package volcengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
	"time"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/NSObjects/go-speech/tts"
)

const (
	closedReason  = "TTS_STREAM_FINAL_REASON_CLOSED"
	flushedReason = "TTS_STREAM_FINAL_REASON_FLUSHED"
)

type remoteError struct {
	Type      string `json:"type"`
	Code      string `json:"code"`
	RequestID string `json:"request_id"`
	LogID     string `json:"logid"`
	WSLogID   string `json:"log_id"`
}

type event struct {
	Event     string            `json:"event"`
	SessionID string            `json:"session_id,omitempty"`
	Audio     []byte            `json:"audio,omitempty"`
	IsFinal   bool              `json:"is_final,omitempty"`
	Reason    string            `json:"reason,omitempty"`
	Error     *remoteError      `json:"error,omitempty"`
	Text      string            `json:"text,omitempty"`
	Words     []timedWord       `json:"words,omitempty"`
	Phonemes  []json.RawMessage `json:"phonemes,omitempty"`
}

type timedWord struct {
	Word      string   `json:"word"`
	StartTime *float64 `json:"startTime"`
	EndTime   *float64 `json:"endTime"`
}

// AlignmentDetails preserves native phonemes because the service does not
// specify a stable schema for individual phoneme objects.
type AlignmentDetails struct {
	Phonemes []json.RawMessage
}

func (e event) alignment() (*tts.Alignment, error) {
	a := &tts.Alignment{Text: e.Text}
	for _, w := range e.Words {
		if w.StartTime == nil || w.EndTime == nil ||
			*w.StartTime < 0 || *w.EndTime < *w.StartTime ||
			*w.EndTime >= float64(math.MaxInt64)/float64(time.Second) {
			return nil, protocol("invalid alignment timestamps")
		}
		a.Words = append(a.Words, tts.Word{
			Text: w.Word, StartTime: time.Duration(*w.StartTime * float64(time.Second)),
			EndTime: time.Duration(*w.EndTime * float64(time.Second)),
		})
	}
	if e.Phonemes != nil {
		a.Provider = AlignmentDetails{Phonemes: e.Phonemes}
	}
	return a, nil
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
	// Include only bounded diagnostic identifiers, never arbitrary messages
	// that may echo the synthesis text or credentials.
	logID := e.Error.LogID
	if logID == "" {
		logID = e.Error.WSLogID
	}
	return &speecherr.Error{
		Code: code, Provider: "volcengine", Operation: "seed3_receive",
		RemoteCode: diagnosticID(e.Error.Code),
		Err: fmt.Errorf("type=%s code=%s request_id=%s log_id=%s",
			diagnosticID(e.Error.Type), diagnosticID(e.Error.Code),
			diagnosticID(e.Error.RequestID), diagnosticID(logID)),
	}
}

func diagnosticID(value string) string {
	if len(value) == 0 || len(value) > 128 {
		return "-"
	}
	for _, ch := range value {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' ||
			ch >= '0' && ch <= '9' || ch == '_' || ch == '-' || ch == '.') {
			return "-"
		}
	}
	return value
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
	mode      receiveMode
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

type receiveMode uint8

const (
	receiveAudio receiveMode = iota + 1
	receiveEvents
)

var _ tts.EventReceiver = (*stream)(nil)

func (s *stream) fail(err error) (tts.Event, error) {
	s.terminal = err
	_ = s.Close()
	return tts.Event{}, err
}

func (s *stream) Recv(ctx context.Context) (audio.Chunk, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mode == receiveEvents {
		return audio.Chunk{}, tts.ErrReceiveMode
	}
	s.mode = receiveAudio
	for {
		e, err := s.recvEvent(ctx)
		if err != nil {
			return audio.Chunk{}, err
		}
		if e.Type == tts.EventAudio {
			return *e.Audio, nil
		}
	}
}

func (s *stream) RecvEvent(ctx context.Context) (tts.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mode == receiveAudio {
		return tts.Event{}, tts.ErrReceiveMode
	}
	s.mode = receiveEvents
	return s.recvEvent(ctx)
}

// recvEvent is called with mu held; it is the single protocol reader.
func (s *stream) recvEvent(ctx context.Context) (tts.Event, error) {
	if s.terminal != nil {
		return tts.Event{}, s.terminal
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
			chunk := &audio.Chunk{Format: s.format, Data: e.Audio}
			return tts.Event{Type: tts.EventAudio, Audio: chunk}, nil
		case "alignment":
			if s.mode == receiveAudio {
				continue
			}
			if !s.ready {
				return s.fail(protocol("alignment before ready"))
			}
			a, err := e.alignment()
			if err != nil {
				return s.fail(err)
			}
			return tts.Event{Type: tts.EventAlignment, Alignment: a}, nil
		case "final":
			if !s.ready || !e.IsFinal {
				return s.fail(protocol("invalid final event"))
			}
			switch e.Reason {
			case flushedReason:
				return tts.Event{Type: tts.EventBatchCompleted}, nil
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
