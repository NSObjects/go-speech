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
	terminalErr error
	finalSeen   map[int]struct{}
	queue       []asr.Event // readMu; one server message may contain multiple sentences
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
func (s *Stream) closeError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.parent.Err(); err != nil {
		return err
	}
	return asr.ErrStreamClosed
}
func (s *Stream) fail(err error) error {
	if s.terminalErr == nil {
		s.terminalErr = err
	}
	_ = s.Close()
	return s.terminalErr
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
	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
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

// Send sends complete PCM16LE samples. Tencent's real-time service expects
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
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(data) == 0 || len(data)%2 != 0 {
		return argumentError("PCM16LE audio must contain complete 16-bit samples")
	}
	if err := s.write(ctx, websocket.BinaryMessage, data); err != nil {
		result := s.transportError(ctx, "send", err)
		_ = s.Close()
		return result
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
		return err
	}
	if !s.sentAudio {
		return argumentError("cannot close empty audio input")
	}
	if err := s.write(ctx, websocket.TextMessage, []byte(`{"type":"end"}`)); err != nil {
		result := s.transportError(ctx, "finish", err)
		_ = s.Close()
		return result
	}
	s.sendClosed = true
	return nil
}

type response struct {
	Code      int           `json:"code"`
	Message   string        `json:"message"`
	VoiceID   string        `json:"voice_id"`
	Final     int           `json:"final"`
	Result    *segment      `json:"result"`
	Sentences *sentenceList `json:"sentences"`
}
type segment struct {
	SliceType int    `json:"slice_type"`
	Index     int    `json:"index"`
	Text      string `json:"voice_text_str"`
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
}

func decodeResponse(raw []byte, expectedID string) (response, error) {
	var result response
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, &speecherr.Error{Code: speecherr.CodeProtocol, Provider: "tencent", Operation: "decode", Err: err}
	}
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
		Retryable:  code == speecherr.CodeUnavailable || code == speecherr.CodeRateLimited || code == speecherr.CodeTimeout,
		// Tencent's text is not treated as a stable SDK contract.
		Err: fmt.Errorf("Tencent ASR error %d: %s", r.Code, r.Message),
	}
}

func (s *Stream) awaitHandshake(ctx context.Context, timeout time.Duration) error {
	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
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
	result, err := decodeResponse(raw, s.voiceID)
	if err != nil {
		return err
	}
	if result.Final != 0 || result.Result != nil {
		return protocolError("unexpected recognition event during handshake")
	}
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
	defer s.readMu.Unlock()
	if s.terminalErr != nil {
		return asr.Event{}, s.terminalErr
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
		stop := context.AfterFunc(ctx, func() { _ = s.Close() })
		var deadline time.Time
		if d, ok := ctx.Deadline(); ok {
			deadline = d
		}
		if err := s.conn.SetReadDeadline(deadline); err != nil {
			stop()
			return asr.Event{}, s.fail(s.transportError(ctx, "recv", err))
		}
		typ, raw, err := s.conn.ReadMessage()
		stop()
		if err != nil {
			return asr.Event{}, s.fail(s.transportError(ctx, "recv", err))
		}
		if typ != websocket.TextMessage {
			return asr.Event{}, s.fail(protocolError("non-text ASR response"))
		}
		message, err := decodeResponse(raw, s.voiceID)
		if err != nil {
			return asr.Event{}, s.fail(err)
		}
		if message.Result != nil && message.Sentences != nil {
			return asr.Event{}, s.fail(protocolError("conflicting ASR response layouts"))
		}
		if r := message.Result; r != nil {
			switch r.SliceType {
			case 0:
				s.queue = append(s.queue, asr.Event{Type: asr.EventSpeechStart})
			case 1:
				if _, finalized := s.finalSeen[r.Index]; !finalized && r.Text != "" {
					s.queue = append(s.queue, asr.Event{Type: asr.EventPartial, Text: r.Text})
				}
			case 2:
				if _, already := s.finalSeen[r.Index]; !already {
					s.finalSeen[r.Index] = struct{}{}
					if r.Text != "" {
						s.queue = append(s.queue, asr.Event{Type: asr.EventFinal, Text: r.Text})
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
						s.queue = append(s.queue, asr.Event{Type: asr.EventPartial, Text: sentence.Text})
					}
				case 1:
					if _, already := s.finalSeen[sentence.ID]; !already {
						s.finalSeen[sentence.ID] = struct{}{}
						if sentence.Text != "" {
							s.queue = append(s.queue, asr.Event{Type: asr.EventFinal, Text: sentence.Text})
						}
					}
				default:
					return asr.Event{}, s.fail(protocolError("unknown sentence_type"))
				}
			}
		}
		if message.Final == 1 {
			s.finished = true
			_ = s.Close()
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
