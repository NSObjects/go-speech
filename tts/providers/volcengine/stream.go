package volcengine

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
	"github.com/NSObjects/go-speech/tts"
	"github.com/gorilla/websocket"
)

// Stream is a single V3 synthesis. Only one Recv caller is supported.
type Stream struct {
	conn *websocket.Conn
	parent context.Context
	format audio.Format
	done chan struct{}
	once sync.Once
	mu sync.Mutex
	finished bool
	audioSeen bool
	terminalErr error
}

var _ tts.Stream = (*Stream)(nil)

func newStream(conn *websocket.Conn, ctx context.Context, format audio.Format) *Stream {
	s := &Stream{
		conn: conn, parent: ctx, format: format, done: make(chan struct{}),
	}
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
	s.once.Do(func() {
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

func (s *Stream) fail(err error) error {
	if s.terminalErr == nil {
		s.terminalErr = err
	}
	_ = s.Close()
	return s.terminalErr
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

func (s *Stream) networkError(ctx context.Context, op string, err error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if e := s.parent.Err(); e != nil {
		return e
	}
	var ne net.Error
	if d, ok := ctx.Deadline(); ok && !time.Now().Before(d) &&
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
		Code: code, Provider: "volcengine", Operation: op,
		Err: err, Retryable: true,
	}
}

func (s *Stream) sendText(ctx context.Context, req tts.Request, encoding string, timeout time.Duration) error {
	body, err := json.Marshal(map[string]any{
		"user": map[string]any{"uid": "go-speech"},
		"req_params": map[string]any{
			"text": req.Text,
			"speaker": req.Voice,
			"audio_params": map[string]any{
				"format": encoding,
				"sample_rate": req.Format.SampleRate,
			},
		},
	})
	if err != nil {
		return err
	}
	frame, err := encodeRequest(body)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.parent.Err(); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
	defer stop()
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := s.conn.SetWriteDeadline(deadline); err != nil {
		return s.networkError(ctx, "send", err)
	}
	if err := s.conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		return s.networkError(ctx, "send", err)
	}
	return nil
}

// Recv returns raw audio event 352 bytes and stable EOF only after provider
// SessionFinished event 152 with successful status_code 20000000.
func (s *Stream) Recv(ctx context.Context) (audio.Chunk, error) {
	if s == nil {
		return audio.Chunk{}, tts.ErrStreamClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminalErr != nil {
		return audio.Chunk{}, s.terminalErr
	}
	if s.finished {
		return audio.Chunk{}, io.EOF
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
		var deadline time.Time
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
		if typ != websocket.BinaryMessage {
			return audio.Chunk{}, s.fail(protocol("expected binary V3 response"))
		}
		frame, err := parseResponse(data)
		if err != nil {
			return audio.Chunk{}, s.fail(err)
		}
		if frame.kind == kindServerError {
			return audio.Chunk{}, s.fail(frame.failure())
		}
		if frame.event == eventSessionFinished {
			if frame.kind != kindFullResponse {
				return audio.Chunk{}, s.fail(protocol("invalid SessionFinished response type"))
			}
			if err := frame.sessionStatus(); err != nil {
				return audio.Chunk{}, s.fail(err)
			}
			if !s.audioSeen {
				return audio.Chunk{}, s.fail(protocol("synthesis completed without audio"))
			}
			s.finished = true
			_ = s.Close()
			return audio.Chunk{}, io.EOF
		}
		if frame.event == eventTTSAudio {
			if frame.kind != kindAudioResponse {
				return audio.Chunk{}, s.fail(protocol("invalid TTSResponse audio type"))
			}
			if len(frame.payload) > 0 {
				s.audioSeen = true
				return audio.Chunk{Data: frame.payload, Format: s.format}, nil
			}
			continue
		}
		// Sentence start/end and other optional metadata contain no audio.
		if frame.kind == kindAudioResponse {
			return audio.Chunk{}, s.fail(protocol("unexpected audio response event"))
		}
	}
}
