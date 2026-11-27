package tencent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/gorilla/websocket"
)

// Stream represents a single Tencent WebSocket V2 ASR session. Exactly one
// Send caller and one Recv caller may operate concurrently.
type Stream struct {
	conn        *websocket.Conn
	parent      context.Context
	voiceID     string
	done        chan struct{}
	once        sync.Once
	writeMu     sync.Mutex
	readMu      sync.Mutex
	sendClosed  bool
	sentAudio   bool
	finished    bool
	terminalErr error // terminalMu
	terminalSuccess bool // terminalMu: provider completion cannot be overwritten
	finalSeen   map[int]struct{}
	queue       []asr.Event // readMu; one server message may contain multiple sentences

	terminalMu      sync.Mutex    // terminalErr can be set by Send, CloseSend, or Recv
	encoding        audio.Encoding // immutable after OpenStream handshake; zero is PCM for internal tests
	readIdleTimeout time.Duration // immutable after OpenStream handshake
	details         bool          // immutable
	returnEmpty     bool          // immutable
	metadataMu      sync.Mutex
	speakerContext  string
}

var _ asr.Stream = (*Stream)(nil)

func newStream(conn *websocket.Conn, parent context.Context, voiceID string) *Stream {
	s := &Stream{
		conn: conn, parent: parent, voiceID: voiceID,
		done: make(chan struct{}), finalSeen: make(map[int]struct{}),
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
	_ = s.recordFailure(s.closeError(context.Background()))
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
	s.finalSeen = nil
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
func (s *Stream) closeError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.parent.Err(); err != nil {
		return err
	}
	return asr.ErrStreamClosed
}
func (s *Stream) terminalFailure() error {
	s.terminalMu.Lock()
	defer s.terminalMu.Unlock()
	return s.terminalErr
}

// fail records the first terminal error before closing the socket, so a
// concurrent Recv observes the original Send/CloseSend failure, not an
// incidental "connection closed" caused by cleanup.
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

func (s *Stream) transportError(ctx context.Context, operation string, err error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if e := s.parent.Err(); e != nil {
		return e
	}
	var network net.Error
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) &&
		errors.As(err, &network) && network.Timeout() {
		return context.DeadlineExceeded
	}
	if s.closed() {
		return asr.ErrStreamClosed
	}
	code := speecherr.CodeUnavailable
	if errors.As(err, &network) && network.Timeout() {
		code = speecherr.CodeTimeout
	}
	return &speecherr.Error{
		Code: code, Provider: "tencent", Operation: operation,
		Err: err, Retryable: code == speecherr.CodeUnavailable || code == speecherr.CodeTimeout,
	}
}
func (s *Stream) write(ctx context.Context, typ int, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.parent.Err(); err != nil {
		return err
	}
	if s.closed() {
		return s.closeError(ctx)
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
	return s.conn.WriteMessage(typ, data)
}

// Send sends complete PCM16LE samples, continuous WAV/MP3 fragments, or one
// complete native Opus frame.
// Tencent's real-time service expects
// approximately real-time pacing; the SDK does not artificially sleep or
// silently resample caller-supplied audio.
func (s *Stream) Send(ctx context.Context, data []byte) error {
	if s == nil {
		return asr.ErrStreamClosed
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closed() {
		return s.closeError(ctx)
	}
	if s.sendClosed {
		return asr.ErrInputClosed
	}
	// Cancellation of an upload operation terminates the WebSocket session,
	// even when it happens before the first write.
	if err := ctx.Err(); err != nil {
		return s.failWrite(err)
	}
	if len(data) == 0 {
		return argumentError("audio chunk must not be empty")
	}
	switch s.encoding {
	case audio.EncodingWAV, audio.EncodingMP3:
	case EncodingOpusFrames:
		if _, err := nativeOpusPacket(data); err != nil {
			return err
		}
	default:
		if len(data)%2 != 0 {
			return argumentError("PCM16LE audio must contain complete 16-bit samples")
		}
	}
	if err := s.write(ctx, websocket.BinaryMessage, data); err != nil {
		return s.failWrite(s.transportError(ctx, "send", err))
	}
	s.sentAudio = true
	return nil
}

// CloseSend half-closes the upload by sending Tencent's {"type":"end"} text
// message; the WebSocket stays open for pending recognition results.
func (s *Stream) CloseSend(ctx context.Context) error {
	if s == nil {
		return asr.ErrStreamClosed
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closed() {
		return s.closeError(ctx)
	}
	if s.sendClosed {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return s.failWrite(err)
	}
	if !s.sentAudio {
		return argumentError("cannot close empty audio input")
	}
	if err := s.write(ctx, websocket.TextMessage, []byte(`{"type":"end"}`)); err != nil {
		return s.failWrite(s.transportError(ctx, "finish", err))
	}
	s.sendClosed = true
	return nil
}

type response struct {
	Code             int           `json:"code"`
	Message          string        `json:"message"`
	VoiceID          string        `json:"voice_id"`
	Final            int           `json:"final"`
	Result           *segment      `json:"result"`
	Sentences        *sentenceList `json:"sentences"`
	SpeakerContextID string        `json:"speaker_context_id"`
}
type segment struct {
	SliceType int            `json:"slice_type"`
	Index     int            `json:"index"`
	Text      string         `json:"voice_text_str"`
	StartTime *int64         `json:"start_time"`
	EndTime   *int64         `json:"end_time"`
	Words     []responseWord `json:"word_list"`
	raw       json.RawMessage
}

// Tencent large-model 2.0 WebSocket V2 returns a sentence_list, while
// traditional real-time ASR returns result.slice_type. Both use /asr/v2.
type sentenceList struct {
	SentenceList []sentence `json:"sentence_list"`
}
type sentence struct {
	Text string `json:"sentence"`
	Type int    `json:"sentence_type"`
	ID   int    `json:"sentence_id"`
	// Pointer preserves absent speaker_id vs Tencent's -1 (pending) and 0.
	SpeakerID *int   `json:"speaker_id"`
	StartTime *int64 `json:"start_time"`
	EndTime   *int64 `json:"end_time"`
}

func decodeResponse(raw []byte, expectedID string, details bool) (response, error) {
	var result response
	var status *int
	var err error
	if details {
		// RawMessage owns a copy of the native result only when requested.
		var wire struct {
			*response
			Code   *int            `json:"code"`
			Result json.RawMessage `json:"result"`
		}
		wire.response = &result
		err = json.Unmarshal(raw, &wire)
		status = wire.Code
		if err == nil && len(wire.Result) != 0 {
			err = json.Unmarshal(wire.Result, &result.Result)
			if err == nil && result.Result != nil {
				result.Result.raw = wire.Result
			}
		}
	} else {
		var wire struct {
			*response
			Code *int `json:"code"`
		}
		wire.response = &result
		err = json.Unmarshal(raw, &wire)
		status = wire.Code
	}
	if err != nil {
		return result, &speecherr.Error{Code: speecherr.CodeProtocol, Provider: "tencent", Operation: "decode", Err: err}
	}
	// A missing/null code must not be interpreted as success.
	if status == nil {
		return result, protocolError("missing or null response code")
	}
	result.Code = *status
	if result.VoiceID != expectedID {
		return result, protocolError("voice_id mismatch or missing")
	}
	if result.Code != 0 {
		return result, remoteError(result)
	}
	if result.Final != 0 && result.Final != 1 {
		return result, protocolError("invalid final flag")
	}
	return result, nil
}

func remoteError(r response) error {
	code := speecherr.CodeInternal
	switch r.Code {
	case 4000, 4001, 4007:
		code = speecherr.CodeInvalidArgument
	case 4002, 4003, 4005, 6001:
		code = speecherr.CodeUnauthorized
	case 4004, 4006:
		code = speecherr.CodeRateLimited
	case 4008:
		code = speecherr.CodeTimeout
	case 4009, 5000, 5001, 5002:
		code = speecherr.CodeUnavailable
	case 4010:
		code = speecherr.CodeProtocol
	}
	return &speecherr.Error{
		Code: code, Provider: "tencent", Operation: "recognize",
		RemoteCode: strconv.Itoa(r.Code),
		// 4008 means the client stopped uploading audio; 4009 means the
		// client connection closed. Repeating an unchanged request will not
		// fix either condition. 4004 needs quota/billing remediation.
		// Only transient server failures (500x) or concurrency throttling
		// (4006) warrant retrying a new session, never replaying audio here.
		Retryable: r.Code == 4006 || r.Code == 5000 || r.Code == 5001 || r.Code == 5002,
		// Tencent's text is not treated as a stable SDK contract.
		Err: fmt.Errorf("Tencent ASR error %d: %s", r.Code, r.Message),
	}
}

func (s *Stream) awaitHandshake(ctx context.Context, timeout time.Duration) error {
	stop := context.AfterFunc(ctx, func() { _ = s.fail(ctx.Err()) })
	defer stop()
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := s.conn.SetReadDeadline(deadline); err != nil {
		return s.transportError(ctx, "handshake", err)
	}
	typ, raw, err := s.conn.ReadMessage()
	if err != nil {
		return s.transportError(ctx, "handshake", err)
	}
	if typ != websocket.TextMessage {
		return protocolError("expected text handshake")
	}
	result, err := decodeResponse(raw, s.voiceID, false)
	if err != nil {
		return err
	}
	// Session creation must not consume recognition events. A nonempty V2
	// sentence_list is speech output even when the enclosing final flag is 0.
	// An empty sentence list is harmless startup metadata, so accept it.
	if result.Final != 0 || result.Result != nil ||
		(result.Sentences != nil && len(result.Sentences.SentenceList) > 0) {
		return protocolError("unexpected recognition event during handshake")
	}
	s.updateSpeakerContext(result.SpeakerContextID)
	if err := s.conn.SetReadDeadline(time.Time{}); err != nil {
		return s.transportError(ctx, "handshake", err)
	}
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
	if err := s.terminalFailure(); err != nil {
		return asr.Event{}, err
	}
	// Only a provider-confirmed final frame may drain queued events after
	// the socket closes. An explicit Close must discard pending events.
	if !s.finished && s.closed() {
		return asr.Event{}, s.fail(s.closeError(ctx))
	}
	// Pending V2 sentences must not mask cancellation of a new Recv call.
	// A provider-confirmed completion is already terminal and may drain
	// its queued results regardless of a later canceled caller context.
	if !s.finished {
		if err := ctx.Err(); err != nil {
			return asr.Event{}, s.fail(err)
		}
		if err := s.parent.Err(); err != nil {
			return asr.Event{}, s.fail(err)
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
	for {
		if err := ctx.Err(); err != nil {
			return asr.Event{}, s.fail(err)
		}
		if err := s.parent.Err(); err != nil {
			return asr.Event{}, s.fail(err)
		}
		if s.closed() {
			return asr.Event{}, s.fail(s.closeError(ctx))
		}
		stop := context.AfterFunc(ctx, func() { _ = s.fail(ctx.Err()) })
		var deadline time.Time
		if d, ok := ctx.Deadline(); ok {
			deadline = d
		}
		if s.readIdleTimeout > 0 {
			idleDeadline := time.Now().Add(s.readIdleTimeout)
			if deadline.IsZero() || idleDeadline.Before(deadline) {
				deadline = idleDeadline
			}
		}
		if err := s.conn.SetReadDeadline(deadline); err != nil {
			stop()
			return asr.Event{}, s.fail(s.transportError(ctx, "recv", err))
		}
		typ, raw, err := s.conn.ReadMessage()
		stop()
		if cause := s.terminalFailure(); cause != nil {
			return asr.Event{}, cause
		}
		if err != nil {
			return asr.Event{}, s.fail(s.transportError(ctx, "recv", err))
		}
		if typ != websocket.TextMessage {
			return asr.Event{}, s.fail(protocolError("non-text ASR response"))
		}
		message, err := decodeResponse(raw, s.voiceID, s.details)
		if err != nil {
			return asr.Event{}, s.fail(err)
		}
		s.updateSpeakerContext(message.SpeakerContextID)
		if message.Result != nil && message.Sentences != nil {
			return asr.Event{}, s.fail(protocolError("conflicting ASR response layouts"))
		}
		if r := message.Result; r != nil {
			switch r.SliceType {
			case 0:
				s.queue = append(s.queue, s.segmentEvent(asr.EventSpeechStart, r))
			case 1:
				if _, finalized := s.finalSeen[r.Index]; !finalized && r.Text != "" {
					s.queue = append(s.queue, s.segmentEvent(asr.EventPartial, r))
				}
			case 2:
				if _, already := s.finalSeen[r.Index]; !already {
					s.finalSeen[r.Index] = struct{}{}
					if r.Text != "" || s.returnEmpty {
						s.queue = append(s.queue, s.segmentEvent(asr.EventFinal, r))
					}
				}
			default:
				return asr.Event{}, s.fail(protocolError("unknown slice_type"))
			}
		}
		if list := message.Sentences; list != nil {
			for _, sentence := range list.SentenceList {
				switch sentence.Type {
				case 0:
					if _, finalized := s.finalSeen[sentence.ID]; !finalized && sentence.Text != "" {
						s.queue = append(s.queue, s.sentenceEvent(asr.EventPartial, sentence))
					}
				case 1:
					if _, already := s.finalSeen[sentence.ID]; !already {
						s.finalSeen[sentence.ID] = struct{}{}
						if sentence.Text != "" {
							s.queue = append(s.queue, s.sentenceEvent(asr.EventFinal, sentence))
						}
					}
				default:
					return asr.Event{}, s.fail(protocolError("unknown sentence_type"))
				}
			}
		}
		if message.Final == 1 {
			if err := s.complete(); err != nil {
				return asr.Event{}, err
			}
		}
		if cause := s.terminalFailure(); cause != nil {
			return asr.Event{}, cause
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
	}
}

// failWrite runs outside readMu. Release receive state even if the caller
// has no active receiver, after saving the first terminal error.
func (s *Stream) failWrite(err error) error {
	first := s.fail(err)
	_ = s.Close()
	return first
}
