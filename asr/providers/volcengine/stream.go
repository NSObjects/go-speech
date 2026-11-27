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
	terminalMu sync.Mutex
	sendClosed bool
	sentAudio  bool
	pcmFrameBytes int // 0 for encoded formats; immutable after OpenStream

	finished       bool                // readMu
	terminalErr    error               // terminalMu
	terminalSuccess bool // terminalMu: provider completion cannot be overwritten
	queue          []asr.Event         // readMu
	resultType     string              // immutable
	pendingDetails *asr.Utterance      // readMu
	details        bool                // immutable
	pendingText    string              // readMu: latest unconfirmed utterance
	confirmedText  strings.Builder              // readMu: concatenated confirmed segments
	lastFinalText  string              // readMu: most recent confirmed utterance
	finalSeen      map[string]struct{} // readMu
	finalMetadata  map[string]finalMetadata // readMu; detailed timed finals only
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
			_ = s.fail(parent.Err())
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
	_ = s.recordFailure(s.closedError(context.Background()))
	// Unblock the receiver before acquiring its lock. Recv itself uses
	// closeTransport to avoid reentering readMu.
	s.closeTransport()
	s.readMu.Lock()
	s.releaseReadState()
	s.readMu.Unlock()
	return nil
}

func (s *Stream) closeTransport() {
	s.once.Do(func() {
		close(s.done)
		_ = s.conn.Close()
	})
}

// releaseReadState requires readMu. A successful final frame may still
// contain events the caller has not received; preserve that backlog.
func (s *Stream) releaseReadState() {
	s.confirmedText.Reset()
	s.lastFinalText = ""
	s.pendingText = ""
	s.pendingDetails = nil
	s.finalSeen = nil
	s.finalMetadata = nil
	if !s.finished || s.terminalFailure() != nil || len(s.queue) == 0 {
		s.queue = nil
	}
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
	stop := context.AfterFunc(ctx, func() { _ = s.fail(ctx.Err()) })
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
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closed() {
		return s.closedError(ctx)
	}
	if s.sendClosed {
		return asr.ErrInputClosed
	}
	// A canceled upload operation cannot leave a reusable provider session.
	if err := ctx.Err(); err != nil {
		return s.failWrite(err)
	}
	if len(data) == 0 {
		return argumentError("audio chunk cannot be empty")
	}
	// Reject incomplete PCM frames before charging/uploading to the provider.
	// Ogg Opus, MP3 and WAV bytes remain freely chunkable.
	if s.pcmFrameBytes > 0 && len(data)%s.pcmFrameBytes != 0 {
		return argumentError("PCM16LE chunk must contain complete audio frames")
	}
	frame, err := encodeFrame(messageAudioRequest, 0, 0, data)
	if err != nil {
		return &speecherr.Error{Code: speecherr.CodeInvalidArgument, Provider: "volcengine", Operation: "send", Err: err}
	}
	if err := s.writeFrame(ctx, frame); err != nil {
		return s.failWrite(s.transportError(ctx, "send", err))
	}
	s.sentAudio = true
	return nil
}

func (s *Stream) CloseSend(ctx context.Context) error {
	if s == nil {
		return asr.ErrStreamClosed
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closed() {
		return s.closedError(ctx)
	}
	if s.sendClosed {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return s.failWrite(err)
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
		return s.failWrite(s.transportError(ctx, "finish", err))
	}
	s.sendClosed = true
	return nil
}

func (s *Stream) Recv(ctx context.Context) (asr.Event, error) {
	if s == nil {
		return asr.Event{}, asr.ErrStreamClosed
	}
	s.readMu.Lock()
	defer func() {
		if s.closed() {
			s.releaseReadState()
		}
		s.readMu.Unlock()
	}()
	for {
		if err := s.terminalFailure(); err != nil {
			// A cancellation callback may have published the error but not
			// yet closed the transport. Wait for the idempotent close before
			// exposing a terminal result to the caller.
			return asr.Event{}, s.fail(err)
		}
		// Only a provider-confirmed final response may drain queued events
		// after the connection closes. An active stream must honor Close and
		// Recv cancellation before delivering previously buffered results.
		if !s.finished {
			if err := ctx.Err(); err != nil {
				return asr.Event{}, s.fail(err)
			}
			if err := s.parent.Err(); err != nil {
				return asr.Event{}, s.fail(err)
			}
			if s.closed() {
				return asr.Event{}, s.fail(asr.ErrStreamClosed)
			}
		}
		if len(s.queue) > 0 {
			e := s.queue[0]
			s.queue[0] = asr.Event{}
			s.queue = s.queue[1:]
			return e, nil
		}
		if s.finished {
			return asr.Event{}, io.EOF
		}
		stop := context.AfterFunc(ctx, func() { _ = s.fail(ctx.Err()) })
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
		if cause := s.terminalFailure(); cause != nil {
			return asr.Event{}, s.fail(cause)
		}
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
		results, last, noSpeech, err := message.result()
		if err != nil {
			return asr.Event{}, s.fail(err)
		}
		if noSpeech {
			// An explicit provider no-speech result must not upgrade an
			// earlier provisional hypothesis to a final utterance.
			s.pendingText = ""
			s.pendingDetails = nil
		} else {
			s.addEvents(results, last)
		}
		if last {
			if err := s.complete(); err != nil {
				return asr.Event{}, err
			}
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
	var partialDetails *asr.Utterance
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
				if s.details {
					partialDetails = u.metadata()
				} else {
					partialDetails = nil
				}
				continue
			}
			// A finalized utterance is identified by its time interval, not by
			// transcript text. Full snapshots may revise punctuation or
			// spelling for an already emitted interval; EventFinal is
			// immutable and must not be emitted twice for that segment.
			// If timing is absent, use text to avoid collapsing distinct
			// untimed utterances into the same 0:0 key.
			key := "text:" + u.Text
			if u.Start != 0 || u.End != 0 {
				key = fmt.Sprintf("time:%d:%d", u.Start, u.End)
			}
			if _, alreadyEmitted := s.finalSeen[key]; alreadyEmitted {
				if s.details {
					s.updateFinalMetadata(key, u)
				}
				continue
			}
			// Only a new final supersedes the provisional hypothesis.
			// A replay of an older final (including a metadata update)
			// must not discard a newer partial in this response.
			partial = ""
			partialDetails = nil
			s.finalSeen[key] = struct{}{}
			e := asr.Event{Type: asr.EventFinal, Text: u.Text}
			if s.details {
				e.Utterance = u.metadata()
				e.SpeakerID = e.Utterance.SpeakerID
				s.rememberFinalMetadata(key, u, e.Utterance)
			}
			s.queue = append(s.queue, e)
			if s.resultType == ResultFull {
				s.confirmedText.WriteString(u.Text)
			}
			s.lastFinalText = u.Text
			s.pendingText = ""
			s.pendingDetails = nil
		}
	}

	// If utterance metadata is unavailable, fall back to the text field.
	// The default full mode repeats confirmed text, so remove that prefix
	// before exposing a new tentative segment.
	if !hasUtterances && fallback != "" {
		if s.resultType == ResultFull {
			// A small suffix must not retain the backing storage of a large
			// cumulative provider response.
			suffix := strings.TrimPrefix(fallback, s.confirmedText.String())
			if len(suffix) != len(fallback) {
				fallback = strings.Clone(suffix)
			}
		}
		if fallback != s.lastFinalText {
			partial = fallback
		}
	}
	if partial != "" && partial != s.pendingText {
		// Never emit a provisional event after the terminal response.
		if !last {
			s.queue = append(s.queue, detailedEvent(asr.EventPartial, partial, cloneUtterance(partialDetails)))
		}
		s.pendingText = partial
		s.pendingDetails = partialDetails
	} else if partial != "" && partialDetails != nil {
		s.pendingDetails = partialDetails
	}
	if last && s.pendingText != "" {
		// The provider may omit utterance metadata in its last response.
		// Preserve the final unconfirmed segment instead of losing speech.
		s.queue = append(s.queue, detailedEvent(asr.EventFinal, s.pendingText, s.pendingDetails))
		s.pendingText = ""
		s.pendingDetails = nil
	}
}

// terminalFailure reads the first terminal error, shared by Send,
// CloseSend and Recv without holding the potentially blocked read lock.
func (s *Stream) terminalFailure() error {
	s.terminalMu.Lock()
	defer s.terminalMu.Unlock()
	return s.terminalErr
}

// fail saves the first terminal failure before closing the connection. It
// cannot acquire readMu or writeMu, since Recv may be blocked on ReadMessage.
func (s *Stream) fail(err error) error {
	first := s.recordFailure(err)
	s.closeTransport()
	return first
}

// recordFailure arbitrates failures with successful provider completion.
// A late sender error is still returned to that sender, but must not
// replace the already committed receive-side EOF or final backlog.
func (s *Stream) recordFailure(err error) error {
	s.terminalMu.Lock()
	defer s.terminalMu.Unlock()
	if s.terminalSuccess {
		return err
	}
	if s.terminalErr == nil {
		s.terminalErr = err
	}
	return s.terminalErr
}

// complete is called under readMu after validating the terminal frame.
// Publish success before closing the transport, so late cancellation or
// write errors cannot turn a completed recognition into a failure.
func (s *Stream) complete() error {
	s.terminalMu.Lock()
	if s.terminalErr != nil {
		err := s.terminalErr
		s.terminalMu.Unlock()
		return err
	}
	s.terminalSuccess = true
	s.terminalMu.Unlock()
	s.finished = true
	s.closeTransport()
	return nil
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
	// A WebSocket read may hit its deadline a moment before the
	// context cancellation timer fires. If the socket timed out at the
	// caller's deadline, preserve context.DeadlineExceeded rather than
	// misclassifying the caller deadline as a retryable provider timeout.
	var timeout net.Error
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) &&
		errors.As(err, &timeout) && timeout.Timeout() {
		return context.DeadlineExceeded
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

// failWrite runs outside readMu. Release receive state even if the caller
// has no active receiver, after saving the first terminal error.
func (s *Stream) failWrite(err error) error {
	first := s.fail(err)
	_ = s.Close()
	return first
}
