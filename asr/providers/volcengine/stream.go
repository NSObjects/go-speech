package volcengine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/gorilla/websocket"
)

const defaultWriteTimeout = 10 * time.Second

// Stream is one binary-protocol ASR session. A single writer and a single
// reader may run concurrently. Its Recv semantics match the shared asr.Stream
// contract: io.EOF is stable after a final response, and terminal errors are
// retained on subsequent calls.
type Stream struct {
	conn   *websocket.Conn
	parent context.Context
	done   chan struct{}
	once   sync.Once

	writeMu    sync.Mutex
	readMu     sync.Mutex
	sendClosed bool
	sentAudio  bool

	finished      bool                // readMu
	terminalErr   error               // readMu
	queue         []asr.Event         // readMu
	resultType    string              // immutable
	pendingText   string              // readMu: latest unconfirmed utterance
	confirmedText string              // readMu: concatenated confirmed segments
	lastFinalText string              // readMu: most recent confirmed utterance
	finalSeen     map[string]struct{} // readMu
}

var _ asr.Stream = (*Stream)(nil)

func newStream(conn *websocket.Conn, parent context.Context, resultType string) *Stream {
	s := &Stream{
		conn: conn, parent: parent, done: make(chan struct{}),
		resultType: resultType, finalSeen: make(map[string]struct{}),
	}
	go func() {
		select {
		case <-parent.Done():
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

func (s *Stream) writeFrame(ctx context.Context, frame []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.parent.Err(); err != nil {
		return err
	}
	if s.closed() {
		return asr.ErrStreamClosed
	}
	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
	defer stop()
	deadline := time.Now().Add(defaultWriteTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := s.conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	return s.conn.WriteMessage(websocket.BinaryMessage, frame)
}

func (s *Stream) Send(ctx context.Context, data []byte) error {
	if s == nil {
		return asr.ErrStreamClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(data) == 0 {
		return argumentError("audio chunk cannot be empty")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.sendClosed {
		return asr.ErrInputClosed
	}
	if s.closed() {
		return s.closedError(ctx)
	}
	frame, err := encodeFrame(messageAudioRequest, 0, 0, data)
	if err != nil {
		return &speecherr.Error{Code: speecherr.CodeInvalidArgument, Provider: "volcengine", Operation: "send", Err: err}
	}
	if err := s.writeFrame(ctx, frame); err != nil {
		result := s.transportError(ctx, "send", err)
		_ = s.Close()
		return result
	}
	s.sentAudio = true
	return nil
}

func (s *Stream) CloseSend(ctx context.Context) error {
	if s == nil {
		return asr.ErrStreamClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.sendClosed {
		return nil
	}
	if s.closed() {
		return s.closedError(ctx)
	}
	if !s.sentAudio {
		return argumentError("cannot finish an empty audio stream")
	}
	// A last audio-only request with zero decoded bytes marks stream EOF.
	// The gzip envelope itself remains nonempty and valid.
	frame, err := encodeFrame(messageAudioRequest, flagLast, 0, nil)
	if err != nil {
		return err
	}
	if err := s.writeFrame(ctx, frame); err != nil {
		result := s.transportError(ctx, "finish", err)
		_ = s.Close()
		return result
	}
	s.sendClosed = true
	return nil
}

func (s *Stream) Recv(ctx context.Context) (asr.Event, error) {
	if s == nil {
		return asr.Event{}, asr.ErrStreamClosed
	}
	s.readMu.Lock()
	defer s.readMu.Unlock()
	for {
		if len(s.queue) > 0 {
			e := s.queue[0]
			s.queue[0] = asr.Event{}
			s.queue = s.queue[1:]
			return e, nil
		}
		if s.finished {
			return asr.Event{}, io.EOF
		}
		if s.terminalErr != nil {
			return asr.Event{}, s.terminalErr
		}
		// A completed stream stays at EOF (or its original terminal error)
		// even when a later caller supplies a canceled context.
		if err := ctx.Err(); err != nil {
			return asr.Event{}, s.fail(err)
		}
		if err := s.parent.Err(); err != nil {
			return asr.Event{}, s.fail(err)
		}
		if s.closed() {
			return asr.Event{}, s.fail(asr.ErrStreamClosed)
		}
		stop := context.AfterFunc(ctx, func() { _ = s.Close() })
		var deadline time.Time
		if d, ok := ctx.Deadline(); ok {
			deadline = d
		}
		if err := s.conn.SetReadDeadline(deadline); err != nil {
			stop()
			return asr.Event{}, s.fail(s.transportError(ctx, "recv", err))
		}
		typ, data, readErr := s.conn.ReadMessage()
		stop()
		if readErr != nil {
			result := s.transportError(ctx, "recv", readErr)
			return asr.Event{}, s.fail(result)
		}
		if typ != websocket.BinaryMessage {
			return asr.Event{}, s.fail(protocolError("non-binary ASR response"))
		}
		message, err := parseFrame(data)
		if err != nil {
			return asr.Event{}, s.fail(err)
		}
		results, last, err := message.result()
		if err != nil {
			return asr.Event{}, s.fail(err)
		}
		s.addEvents(results, last)
		if last {
			s.finished = true
			_ = s.Close()
		}
	}
}

// addEvents converts provider transcript snapshots to the vendor-independent
// events. In "full" mode the provider repeats already finalized utterances
// in subsequent responses; only *new* definite utterances become EventFinal.
// Partial events always describe the current unconfirmed utterance when the
// provider includes utterance metadata. This prevents committing cumulative
// text more than once in a multi-sentence conversation.
func (s *Stream) addEvents(results []recognition, last bool) {
	var partial string
	var fallback string
	hasUtterances := false
	for _, r := range results {
		if len(r.Utterances) > 0 {
			hasUtterances = true
		} else if r.Text != "" {
			fallback = r.Text
		}
		for _, u := range r.Utterances {
			if u.Text == "" {
				continue
			}
			if !u.Definite {
				partial = u.Text
				continue
			}
			key := fmt.Sprintf("%d:%d:%s", u.Start, u.End, u.Text)
			if _, alreadyEmitted := s.finalSeen[key]; alreadyEmitted {
				continue
			}
			s.finalSeen[key] = struct{}{}
			s.queue = append(s.queue, asr.Event{Type: asr.EventFinal, Text: u.Text})
			s.confirmedText += u.Text
			s.lastFinalText = u.Text
			s.pendingText = ""
		}
	}

	// If utterance metadata is unavailable, fall back to the text field.
	// The default full mode repeats confirmed text, so remove that prefix
	// before exposing a new tentative segment.
	if !hasUtterances && fallback != "" {
		if s.resultType == ResultFull {
			fallback = strings.TrimPrefix(fallback, s.confirmedText)
		}
		if fallback != s.lastFinalText {
			partial = fallback
		}
	}
	if partial != "" && partial != s.pendingText {
		// Never emit a provisional event after the terminal response.
		if !last {
			s.queue = append(s.queue, asr.Event{Type: asr.EventPartial, Text: partial})
		}
		s.pendingText = partial
	}
	if last && s.pendingText != "" {
		// The provider may omit utterance metadata in its last response.
		// Preserve the final unconfirmed segment instead of losing speech.
		s.queue = append(s.queue, asr.Event{Type: asr.EventFinal, Text: s.pendingText})
		s.pendingText = ""
	}
}

// fail retains the original reason even if Close subsequently tears down the
// WebSocket. Caller holds readMu.
func (s *Stream) fail(err error) error {
	if s.terminalErr == nil {
		s.terminalErr = err
	}
	_ = s.Close()
	return s.terminalErr
}

func (s *Stream) closedError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.parent.Err(); err != nil {
		return err
	}
	return asr.ErrStreamClosed
}

func (s *Stream) transportError(ctx context.Context, op string, err error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if e := s.parent.Err(); e != nil {
		return e
	}
	if s.closed() || errors.Is(err, asr.ErrStreamClosed) {
		return asr.ErrStreamClosed
	}
	code := speecherr.CodeUnavailable
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		code = speecherr.CodeTimeout
	}
	return &speecherr.Error{
		Code: code, Provider: "volcengine", Operation: op, Err: err,
		Retryable: true,
	}
}
