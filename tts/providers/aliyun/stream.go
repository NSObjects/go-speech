package aliyun

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/NSObjects/go-speech/tts"
	"github.com/gorilla/websocket"
)

// Stream is one synthesized audio result. Only one concurrent Recv is
// supported. Cancellation/Close release the connection; io.EOF and terminal
// failures remain stable across subsequent Recv calls.
type Stream struct {
	conn        *websocket.Conn
	parent      context.Context
	taskID      string
	format      audio.Format
	done        chan struct{}
	closeOnce   sync.Once
	readMu      sync.Mutex
	writeMu     sync.Mutex
	inputClosed bool
	textSent    bool
	finished    bool
	audioSeen   bool
	terminalErr error
}

var _ tts.Stream = (*Stream)(nil)

func newStream(conn *websocket.Conn, ctx context.Context, id string, format audio.Format) *Stream {
	s := &Stream{conn: conn, parent: ctx, taskID: id, format: format, done: make(chan struct{})}
	go func() {
		select {
		case <-ctx.Done():
			_ = s.Close()
		case <-s.done:
		}
	}()
	return s
}
func (s *Stream) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		close(s.done)
		_ = s.conn.Close()
	})
	return nil
}
func (s *Stream) closed() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}
func (s *Stream) closeError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.parent.Err(); err != nil {
		return err
	}
	return tts.ErrStreamClosed
}

// fail is called only under readMu and remembers the first terminal failure.
func (s *Stream) fail(err error) error {
	if s.terminalErr == nil {
		s.terminalErr = err
	}
	_ = s.Close()
	return s.terminalErr
}

func (s *Stream) networkError(ctx context.Context, op string, err error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if e := s.parent.Err(); e != nil {
		return e
	}
	var ne net.Error
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) &&
		errors.As(err, &ne) && ne.Timeout() {
		return context.DeadlineExceeded
	}
	if s.closed() {
		return tts.ErrStreamClosed
	}
	code := speecherr.CodeUnavailable
	if errors.As(err, &ne) && ne.Timeout() {
		code = speecherr.CodeTimeout
	}
	return &speecherr.Error{
		Code: code, Provider: "aliyun", Operation: op, Err: err,
		Retryable: true,
	}
}

func (s *Stream) sendJSON(ctx context.Context, event any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.parent.Err(); err != nil {
		return err
	}
	if s.closed() {
		return s.closeError(ctx)
	}
	b, err := json.Marshal(event)
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
	defer stop()
	deadline := time.Now().Add(defaultWriteTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := s.conn.SetWriteDeadline(deadline); err != nil {
		return s.networkError(ctx, "write", err)
	}
	if err := s.conn.WriteMessage(websocket.TextMessage, b); err != nil {
		return s.networkError(ctx, "write", err)
	}
	return nil
}
func command(action, id string, payload any) any {
	return map[string]any{
		"header":  map[string]any{"action": action, "task_id": id, "streaming": "duplex"},
		"payload": payload,
	}
}

func (s *Stream) startSession(ctx context.Context, model string, req tts.Request, encoding string, timeout time.Duration) error {
	params := map[string]any{
		"text_type": "PlainText", "voice": req.Voice,
		"format": encoding, "sample_rate": req.Format.SampleRate,
	}
	if req.TextFormat == tts.TextSSML {
		params["enable_ssml"] = true
	}
	start := command("run-task", s.taskID, map[string]any{
		"task_group": "audio", "task": "tts", "function": "SpeechSynthesizer",
		"model": model, "parameters": params, "input": map[string]any{},
	})
	if err := s.sendJSON(ctx, start); err != nil {
		return err
	}

	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := s.conn.SetReadDeadline(deadline); err != nil {
		return s.networkError(ctx, "task-started", err)
	}
	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
	typ, data, err := s.conn.ReadMessage()
	stop()
	if err != nil {
		return s.networkError(ctx, "task-started", err)
	}
	if typ != websocket.TextMessage {
		return protocol("expected task-started text event")
	}
	ev, err := decodeEvent(data, s.taskID)
	if err != nil {
		return err
	}
	switch ev.Event {
	case "task-failed":
		return classifyTaskFailure(ev)
	case "task-started":
		// All subsequent audio reads honor their caller's deadline instead.
	default:
		return protocol("unexpected response to run-task")
	}
	if err := s.conn.SetReadDeadline(time.Time{}); err != nil {
		return s.networkError(ctx, "task-started", err)
	}
	return nil
}

func (s *Stream) start(ctx context.Context, model string, req tts.Request, encoding string, timeout time.Duration) error {
	if err := s.startSession(ctx, model, req, encoding, timeout); err != nil {
		return err
	}
	if err := s.SendText(ctx, req.Text); err != nil {
		return err
	}
	return s.CloseSend(ctx)
}

// SendText adds one text fragment to the active synthesis task.
func (s *Stream) SendText(ctx context.Context, text string) error {
	if s == nil {
		return tts.ErrStreamClosed
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closed() {
		return s.closeError(ctx)
	}
	if s.inputClosed {
		return tts.ErrInputClosed
	}
	if !utf8.ValidString(text) || strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > maxTextRunes {
		return invalid("text fragment must contain 1–20000 valid UTF-8 characters")
	}
	if err := s.sendJSON(ctx, command("continue-task", s.taskID, map[string]any{
		"input": map[string]any{"text": text},
	})); err != nil {
		_ = s.Close()
		return err
	}
	s.textSent = true
	return nil
}

// CloseSend completes text input while Recv continues to receive audio.
func (s *Stream) CloseSend(ctx context.Context) error {
	if s == nil {
		return tts.ErrStreamClosed
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closed() {
		return s.closeError(ctx)
	}
	if s.inputClosed {
		return tts.ErrInputClosed
	}
	if !s.textSent {
		return invalid("at least one text fragment is required")
	}
	if err := s.sendJSON(ctx, command("finish-task", s.taskID, map[string]any{
		"input": map[string]any{},
	})); err != nil {
		_ = s.Close()
		return err
	}
	s.inputClosed = true
	return nil
}

// Recv returns the provider's raw binary audio frame, preserving the actual
// output format. Control events contain no audio and are not exposed as chunks.
func (s *Stream) Recv(ctx context.Context) (audio.Chunk, error) {
	if s == nil {
		return audio.Chunk{}, tts.ErrStreamClosed
	}
	s.readMu.Lock()
	defer s.readMu.Unlock()
	if s.finished {
		return audio.Chunk{}, io.EOF
	}
	if s.terminalErr != nil {
		return audio.Chunk{}, s.terminalErr
	}
	for {
		if err := ctx.Err(); err != nil {
			return audio.Chunk{}, s.fail(err)
		}
		if err := s.parent.Err(); err != nil {
			return audio.Chunk{}, s.fail(err)
		}
		if s.closed() {
			return audio.Chunk{}, s.fail(s.closeError(ctx))
		}
		stop := context.AfterFunc(ctx, func() { _ = s.Close() })
		deadline := time.Time{}
		if d, ok := ctx.Deadline(); ok {
			deadline = d
		}
		if err := s.conn.SetReadDeadline(deadline); err != nil {
			stop()
			return audio.Chunk{}, s.fail(s.networkError(ctx, "recv", err))
		}
		typ, data, err := s.conn.ReadMessage()
		stop()
		if err != nil {
			return audio.Chunk{}, s.fail(s.networkError(ctx, "recv", err))
		}
		switch typ {
		case websocket.BinaryMessage:
			if len(data) != 0 {
				s.audioSeen = true
				return audio.Chunk{Data: data, Format: s.format}, nil
			}
		case websocket.TextMessage:
			ev, err := decodeEvent(data, s.taskID)
			if err != nil {
				return audio.Chunk{}, s.fail(err)
			}
			switch ev.Event {
			case "task-failed":
				return audio.Chunk{}, s.fail(classifyTaskFailure(ev))
			case "task-finished":
				if !s.audioSeen {
					return audio.Chunk{}, s.fail(protocol("synthesis completed without audio"))
				}
				s.finished = true
				_ = s.Close()
				return audio.Chunk{}, io.EOF
			case "result-generated", "task-started":
				// Sentence metadata; the matching audio arrives in binary frames.
			default:
				// Future optional notifications carry no audio; ignore them.
			}
		default:
			return audio.Chunk{}, s.fail(protocol("unexpected WebSocket message type"))
		}
	}
}
