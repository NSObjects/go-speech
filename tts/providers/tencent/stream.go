package tencent

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

// Stream yields consecutive raw audio frames from a single Tencent TTS
// session. A single goroutine may call Recv. Close is idempotent.
type Stream struct {
	conn        *websocket.Conn
	parent      context.Context
	sessionID   string
	format      audio.Format
	done        chan struct{}
	closeOnce   sync.Once
	readMu      sync.Mutex
	writeMu     sync.Mutex
	inputClosed bool
	textSent    bool
	finished    bool
	audioSeen   bool
	terminalMu  sync.Mutex
	terminalErr error
	receiveMode uint8
}

var (
	_ tts.Stream        = (*Stream)(nil)
	_ tts.EventReceiver = (*Stream)(nil)
	_ tts.Resetter      = (*Stream)(nil)
)

const (
	receiveAudio uint8 = iota + 1
	receiveEvents
)

func newStream(conn *websocket.Conn, ctx context.Context, id string, format audio.Format) *Stream {
	s := &Stream{conn: conn, parent: ctx, sessionID: id, format: format, done: make(chan struct{})}
	go func() {
		select {
		case <-ctx.Done():
			_ = s.fail(ctx.Err())
		case <-s.done:
		}
	}()
	return s
}
func (s *Stream) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() { close(s.done); _ = s.conn.Close() })
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
func (s *Stream) fail(err error) error {
	s.terminalMu.Lock()
	if s.terminalErr == nil {
		s.terminalErr = err
	}
	first := s.terminalErr
	s.terminalMu.Unlock()
	_ = s.Close()
	return first
}

func (s *Stream) terminalFailure() error {
	s.terminalMu.Lock()
	defer s.terminalMu.Unlock()
	return s.terminalErr
}

// A sender never reports EOF as a successful write.
func (s *Stream) failWrite(err error) error {
	if first := s.fail(err); !errors.Is(first, io.EOF) {
		return first
	}
	return err
}

func (s *Stream) transportError(ctx context.Context, op string, err error) error {
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
	return &speecherr.Error{Code: code, Provider: "tencent", Operation: op, Err: err, Retryable: true}
}

func (s *Stream) sendCommand(ctx context.Context, action, data string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.parent.Err(); err != nil {
		return err
	}
	if s.closed() {
		return s.closeError(ctx)
	}
	id, err := newID()
	if err != nil {
		return err
	}
	command := map[string]any{
		"session_id": s.sessionID,
		"message_id": id,
		"action":     action,
		"data":       data,
	}
	raw, err := json.Marshal(command)
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = s.fail(ctx.Err()) })
	defer stop()
	deadline := time.Now().Add(10 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := s.conn.SetWriteDeadline(deadline); err != nil {
		return s.transportError(ctx, "write", err)
	}
	if err := s.conn.WriteMessage(websocket.TextMessage, raw); err != nil {
		return s.transportError(ctx, "write", err)
	}
	return nil
}

func (s *Stream) readFrame(ctx context.Context, deadline time.Time) (int, []byte, error) {
	stop := context.AfterFunc(ctx, func() { _ = s.fail(ctx.Err()) })
	defer stop()
	if err := s.conn.SetReadDeadline(deadline); err != nil {
		return 0, nil, s.transportError(ctx, "recv", err)
	}
	typ, data, err := s.conn.ReadMessage()
	if err != nil {
		return 0, nil, s.transportError(ctx, "recv", err)
	}
	return typ, data, nil
}

// start accepts both the initial ACK and the READY event. READY may arrive
// in the same frame as the ACK. Text is sent only after READY, then COMPLETE.
func (s *Stream) startReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	for {
		typ, data, err := s.readFrame(ctx, deadline)
		if cause := s.terminalFailure(); cause != nil {
			return tts.Event{}, cause
		}
		if err != nil {
			return err
		}
		if typ != websocket.TextMessage {
			return protocol("expected text handshake and READY")
		}
		ev, err := decodeEvent(data, s.sessionID)
		if err != nil {
			return err
		}
		if ev.Final == 1 {
			return protocol("FINAL before READY")
		}
		if ev.Ready == 1 {
			break
		}
		// Ignore initial ACK and heartbeat; startup remains bounded.
	}
	return nil
}

func (s *Stream) start(ctx context.Context, text string, timeout time.Duration) error {
	if err := s.startReady(ctx, timeout); err != nil {
		return err
	}
	if err := s.SendText(ctx, text); err != nil {
		return err
	}
	return s.CloseSend(ctx)
}

// SendText sends another text fragment into an active Tencent v2 session.
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
		return invalid("text fragment must contain 1–10000 valid UTF-8 characters")
	}
	if err := s.sendCommand(ctx, "ACTION_SYNTHESIS", text); err != nil {
		return s.failWrite(err)
	}
	s.textSent = true
	return nil
}

// CloseSend finishes text input without stopping audio output.
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
	if err := s.sendCommand(ctx, "ACTION_COMPLETE", ""); err != nil {
		return s.failWrite(err)
	}
	s.inputClosed = true
	return nil
}

// Reset clears only the server's pending text. It does not interrupt audio
// already being synthesized and does not close input.
func (s *Stream) Reset(ctx context.Context) error {
	if s == nil {
		return tts.ErrStreamClosed
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.closed() {
		return s.closeError(ctx)
	}
	if s.inputClosed {
		return tts.ErrInputClosed
	}
	if err := s.sendCommand(ctx, "ACTION_RESET", ""); err != nil {
		return s.failWrite(err)
	}
	// Do not clear textSent: some previously submitted text may already
	// be synthesizing, and CloseSend must still be able to finish it.
	return nil
}

// Recv returns each binary audio frame unchanged and stable EOF after FINAL.
// Control events carry subtitles/metadata, not audio.
func (s *Stream) Recv(ctx context.Context) (audio.Chunk, error) {
	if s == nil {
		return audio.Chunk{}, tts.ErrStreamClosed
	}
	s.readMu.Lock()
	defer s.readMu.Unlock()
	if s.receiveMode == receiveEvents {
		return audio.Chunk{}, tts.ErrReceiveMode
	}
	s.receiveMode = receiveAudio
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

// RecvEvent exposes audio and native alignment events in wire order.
func (s *Stream) RecvEvent(ctx context.Context) (tts.Event, error) {
	if s == nil {
		return tts.Event{}, tts.ErrStreamClosed
	}
	s.readMu.Lock()
	defer s.readMu.Unlock()
	if s.receiveMode == receiveAudio {
		return tts.Event{}, tts.ErrReceiveMode
	}
	s.receiveMode = receiveEvents
	return s.recvEvent(ctx)
}

// recvEvent is the sole protocol reader; readMu must be held.
func (s *Stream) recvEvent(ctx context.Context) (tts.Event, error) {
	if err := s.terminalFailure(); err != nil {
		return tts.Event{}, err
	}
	if s.finished {
		return tts.Event{}, io.EOF
	}
	for {
		if err := ctx.Err(); err != nil {
			return tts.Event{}, s.fail(err)
		}
		if err := s.parent.Err(); err != nil {
			return tts.Event{}, s.fail(err)
		}
		if s.closed() {
			return tts.Event{}, s.fail(s.closeError(ctx))
		}
		var deadline time.Time
		if d, ok := ctx.Deadline(); ok {
			deadline = d
		}
		typ, data, err := s.readFrame(ctx, deadline)
		if err != nil {
			return tts.Event{}, s.fail(err)
		}
		switch typ {
		case websocket.BinaryMessage:
			if len(data) != 0 {
				s.audioSeen = true
				return tts.Event{Type: tts.EventAudio, Audio: &audio.Chunk{Data: data, Format: s.format}}, nil
			}
		case websocket.TextMessage:
			ev, err := decodeEvent(data, s.sessionID)
			if err != nil {
				return tts.Event{}, s.fail(err)
			}
			var alignment *tts.Alignment
			if s.receiveMode == receiveEvents {
				alignment, err = decodeAlignment(data)
				if err != nil {
					return tts.Event{}, s.fail(err)
				}
			}
			if ev.Final == 1 {
				if !s.audioSeen {
					return tts.Event{}, s.fail(protocol("synthesis completed without audio"))
				}
				if err := s.fail(io.EOF); !errors.Is(err, io.EOF) {
					return tts.Event{}, err
				}
				s.finished = true
			}
			if alignment != nil {
				return tts.Event{Type: tts.EventAlignment, Alignment: alignment}, nil
			}
			if s.finished {
				return tts.Event{}, io.EOF
			}
		default:
			return tts.Event{}, s.fail(protocol("unexpected WebSocket message type"))
		}
	}
}
