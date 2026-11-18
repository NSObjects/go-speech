package aliyun

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/gorilla/websocket"
)

var errClosed = errors.New("tts/aliyun: stream closed")

// Stream is a single synthesized audio result. Exactly one Recv caller is
// supported. Explicit Close, parent cancellation and a canceled Recv release
// the network connection; terminal errors remain stable across Recv calls.
type Stream struct {
	conn *websocket.Conn
	parent context.Context
	taskID string
	format audio.Format
	done chan struct{}
	closeOnce sync.Once
	readMu sync.Mutex
	finished bool
	terminalErr error
}

var _ interface {
	Recv(context.Context) (audio.Chunk, error)
	Close() error
} = (*Stream)(nil)

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
	if s == nil { return nil }
	s.closeOnce.Do(func() {
		close(s.done)
		_ = s.conn.Close()
	})
	return nil
}
func (s *Stream) closed() bool {
	select {
	case <-s.done: return true
	default: return false
	}
}
func (s *Stream) closeError(ctx context.Context) error {
	if err := ctx.Err(); err != nil { return err }
	if err := s.parent.Err(); err != nil { return err }
	return errClosed
}
func (s *Stream) fail(err error) error {
	if s.terminalErr == nil { s.terminalErr = err }
	_ = s.Close()
	return s.terminalErr
}

func (s *Stream) sendJSON(ctx context.Context, event any) error {
	if err := ctx.Err(); err != nil { return err }
	if err := s.parent.Err(); err != nil { return err }
	if s.closed() { return errClosed }
	b, err := json.Marshal(event)
	if err != nil { return err }
	stop := context.AfterFunc(ctx, func(){ _ = s.Close() })
	defer stop()
	deadline := time.Now().Add(defaultWriteTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) { deadline = d }
	if err := s.conn.SetWriteDeadline(deadline); err != nil { return s.networkError(ctx, err) }
	if err := s.conn.WriteMessage(websocket.TextMessage, b); err != nil { return s.networkError(ctx, err) }
	return nil
}

func command(action, id string, payload any) any {
	return map[string]any{
		"header": map[string]any{"action": action, "task_id": id, "streaming": "duplex"},
		"payload": payload,
	}
}
func (s *Stream) start(ctx context.Context, model string, req interface {
}, encoding string, timeout time.Duration) error {
	// All request data is sent by startRequest; keep the typed interface
	// to avoid input-text buffering in the Stream.
	return errors.New("not implemented")
}
