package aliyun

import (
	"context"
	"encoding/json"
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

// Alias shared ASR sentinels for backward compatibility with existing clients.
var (
	ErrClosed     = asr.ErrStreamClosed
	ErrSendClosed = asr.ErrInputClosed
)

// Stream is a task-specific WebSocket session. It permits one concurrent sender
// and one concurrent receiver. A canceled Send/Recv context closes the session.
type Stream struct {
	conn        *websocket.Conn
	parent      context.Context
	taskID      string
	done        chan struct{}
	once        sync.Once
	writeMu     sync.Mutex
	readMu      sync.Mutex
	terminalMu  sync.Mutex
	sendClosed  bool  // writeMu
	sentAudio   bool  // writeMu: true after successful audio upload
	finished    bool  // readMu
	terminalErr error // terminalMu: first terminal failure remains observable
	terminalSuccess bool // terminalMu: provider completion cannot be overwritten
	pcmFrameBytes int // 0 for encoded formats; immutable after OpenStream
	details       bool // immutable after OpenStream
	model         string // immutable after OpenStream
	usageMu       sync.Mutex
	usage         *Usage
}

var _ asr.Stream = (*Stream)(nil)

func newStream(conn *websocket.Conn, ctx context.Context, taskID string) *Stream {
	s := &Stream{conn: conn, parent: ctx, taskID: taskID, done: make(chan struct{})}
	go func() {
		select {
		case <-ctx.Done():
			_ = s.fail(ctx.Err())
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
	s.once.Do(func() { close(s.done); _ = s.conn.Close() })
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
func (s *Stream) write(ctx context.Context, typ int, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.parent.Err(); err != nil {
		return err
	}
	if s.closed() {
		return ErrClosed
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
func (s *Stream) writeJSON(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.write(ctx, websocket.TextMessage, b)
}
func (s *Stream) Send(ctx context.Context, chunk []byte) error {
	if s == nil {
		return ErrClosed
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	// A fully closed session must report ErrStreamClosed even if its
	// audio input was previously half-closed (or this chunk is empty).
	if s.closed() {
		return s.closeError(ctx)
	}
	if s.sendClosed {
		return ErrSendClosed
	}
	if err := ctx.Err(); err != nil {
		return s.fail(err)
	}
	if len(chunk) == 0 {
		return badArgument("empty audio chunk")
	}
	// Raw PCM16LE cannot be split in the middle of a sample frame. Other
	// provider-supported encodings are byte streams: no alignment constraint.
	if s.pcmFrameBytes > 0 && len(chunk)%s.pcmFrameBytes != 0 {
		return badArgument("PCM16LE chunk must contain complete audio frames")
	}
	if err := s.write(ctx, websocket.BinaryMessage, chunk); err != nil {
		// Preserve the original write failure for an in-flight Recv, too.
		return s.fail(s.transportError(ctx, "send", err))
	}
	s.sentAudio = true
	return nil
}
func (s *Stream) CloseSend(ctx context.Context) error {
	if s == nil {
		return ErrClosed
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
		return s.fail(err)
	}
	if !s.sentAudio {
		return badArgument("cannot finish an empty audio stream")
	}
	cmd := taskMessage{Header: commandHeader{Action: "finish-task", TaskID: s.taskID, Streaming: "duplex"}, Payload: taskPayload{Input: map[string]any{}}}
	if err := s.writeJSON(ctx, cmd); err != nil {
		return s.fail(s.transportError(ctx, "finish-task", err))
	}
	s.sendClosed = true
	return nil
}
func (s *Stream) Recv(ctx context.Context) (asr.Event, error) {
	if s == nil {
		return asr.Event{}, ErrClosed
	}
	s.readMu.Lock()
	defer s.readMu.Unlock()
	// A concurrent Send/CloseSend failure must not be hidden by task-finished.
	// Preserve the original terminal cause before reporting clean EOF.
	if err := s.terminalFailure(); err != nil {
		return asr.Event{}, err
	}
	if s.finished {
		return asr.Event{}, io.EOF
	}
	if s.closed() {
		return asr.Event{}, s.fail(s.closeError(ctx))
	}
	stop := context.AfterFunc(ctx, func() { _ = s.fail(ctx.Err()) })
	defer stop()
	for {
		if err := ctx.Err(); err != nil {
			return asr.Event{}, s.fail(err)
		}
		if err := s.parent.Err(); err != nil {
			return asr.Event{}, s.fail(err)
		}
		if d, ok := ctx.Deadline(); ok {
			if err := s.conn.SetReadDeadline(d); err != nil {
				result := s.transportError(ctx, "recv", err)
				return asr.Event{}, s.fail(result)
			}
		} else {
			if err := s.conn.SetReadDeadline(time.Time{}); err != nil {
				result := s.transportError(ctx, "recv", err)
				return asr.Event{}, s.fail(result)
			}
		}
		typ, data, err := s.conn.ReadMessage()
		if cause := s.terminalFailure(); cause != nil {
			return asr.Event{}, cause
		}
		if err != nil {
			result := s.transportError(ctx, "recv", err)
			return asr.Event{}, s.fail(result)
		}
		if typ != websocket.TextMessage {
			return asr.Event{}, s.fail(protocolError("recv", errors.New("non-text ASR response")))
		}
		ev, err := decodeEvent(data)
		if err != nil {
			return asr.Event{}, s.fail(protocolError("recv", err))
		}
		if ev.Header.TaskID != s.taskID {
			return asr.Event{}, s.fail(protocolError("recv", errors.New("task_id mismatch")))
		}
		switch ev.Header.Event {
		case "result-generated":
			s.recordUsage(ev.Payload.Usage, false)
			sentence := ev.Payload.Output.Sentence
			if sentence.Heartbeat {
				continue
			}
			if sentence.SentenceEnd {
				if sentence.Text == "" {
					continue
				}
				return s.sentenceEvent(asr.EventFinal, sentence), nil
			}
			if sentence.Text != "" {
				return s.sentenceEvent(asr.EventPartial, sentence), nil
			}
			if sentence.SentenceBegin {
				return s.sentenceEvent(asr.EventSpeechStart, sentence), nil
			}
		case "task-finished":
			s.recordUsage(ev.Payload.Usage, true)
			if err := s.complete(); err != nil {
				return asr.Event{}, err
			}
			return asr.Event{}, io.EOF
		case "task-failed":
			return asr.Event{}, s.fail(taskFailure(ev.Header))
		case "task-started": // Duplicate notification, safe to ignore.
		default: // Forward-compatible optional notifications.
		}
	}
}

// terminalFailure reads a first terminal error shared by the sender and receiver.
func (s *Stream) terminalFailure() error {
	s.terminalMu.Lock()
	defer s.terminalMu.Unlock()
	return s.terminalErr
}

// fail retains the first Send, CloseSend or Recv failure. It must not require
// readMu or writeMu: a sender can fail while a receiver blocks on the socket.
func (s *Stream) fail(err error) error {
	first := s.recordFailure(err)
	_ = s.Close()
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
	_ = s.Close()
	return nil
}

func (s *Stream) transportError(ctx context.Context, op string, err error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if e := s.parent.Err(); e != nil {
		return e
	}
	// The socket can time out at the exact deadline before the context
	// cancellation goroutine runs. Do not turn that caller deadline into
	// a retryable provider error.
	var timeout net.Error
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) &&
		errors.As(err, &timeout) && timeout.Timeout() {
		return context.DeadlineExceeded
	}
	if s.closed() || errors.Is(err, ErrClosed) {
		return ErrClosed
	}
	return wrapNetwork(ctx, op, err)
}

func (s *Stream) closeError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.parent.Err(); err != nil {
		return err
	}
	return ErrClosed
}

type commandHeader struct {
	Action    string `json:"action"`
	TaskID    string `json:"task_id"`
	Streaming string `json:"streaming"`
}
type taskPayload struct {
	TaskGroup  string         `json:"task_group,omitempty"`
	Task       string         `json:"task,omitempty"`
	Function   string         `json:"function,omitempty"`
	Model      string         `json:"model,omitempty"`
	Parameters map[string]any `json:"parameters,omitempty"`
	Input      map[string]any `json:"input"`
}
type taskMessage struct {
	Header  commandHeader `json:"header"`
	Payload taskPayload   `json:"payload"`
}
type responseHeader struct {
	Event        string `json:"event"`
	TaskID       string `json:"task_id"`
	ErrorCode    string `json:"error_code"`
	ErrorMessage string `json:"error_message"`
}
type responseSentence struct {
	Text          string         `json:"text"`
	SentenceBegin bool           `json:"sentence_begin"`
	SentenceEnd   bool           `json:"sentence_end"`
	Heartbeat     bool           `json:"heartbeat"`
	SentenceID    *int64         `json:"sentence_id"`
	BeginTime     *int64         `json:"begin_time"`
	EndTime       *int64         `json:"end_time"`
	Emotion       string         `json:"emo_tag"`
	Confidence    *float64       `json:"emo_confidence"`
	Words         []responseWord `json:"words"`
}
type responsePayload struct {
	Output struct {
		Sentence responseSentence `json:"sentence"`
	} `json:"output"`
	Usage json.RawMessage `json:"usage"`
}
type responseEvent struct {
	Header  responseHeader  `json:"header"`
	Payload responsePayload `json:"payload"`
}

func decodeEvent(data []byte) (responseEvent, error) {
	var ev responseEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return ev, err
	}
	if ev.Header.Event == "" || ev.Header.TaskID == "" {
		return ev, errors.New("missing event or task_id")
	}
	return ev, nil
}
func protocolError(op string, err error) error {
	return &speecherr.Error{Code: speecherr.CodeProtocol, Provider: "aliyun", Operation: op, Err: err}
}
func taskFailure(h responseHeader) error {
	// Classify the stable DashScope error_code before using remote prose.
	// In particular, quota exhaustion requires account remediation, while
	// request-rate throttling may recover when the caller backs off.
	remote := strings.ToLower(strings.TrimSpace(h.ErrorCode))
	code := speecherr.CodeInternal
	retryable := false
	switch {
	case remote == "invalidapikey" || remote == "invalid_api_key" ||
		remote == "authenticationfailed" ||
		remote == "accessdenied" || remote == "access_denied":
		code = speecherr.CodeUnauthorized
	case remote == "invalidparameter" || strings.HasPrefix(remote, "invalidparameter.") ||
		remote == "modelnotfound" || remote == "notfound":
		code = speecherr.CodeInvalidArgument
	case remote == "throttling" || remote == "throttling.ratequota" || remote == "throttling.burstrate" ||
		remote == "limit_requests" || remote == "limit_burst_rate":
		code, retryable = speecherr.CodeRateLimited, true
	case remote == "throttling.allocationquota" || remote == "insufficient_quota" ||
		remote == "arrearage":
		code = speecherr.CodeRateLimited
	case remote == "serviceunavailable" || remote == "service_unavailable":
		code, retryable = speecherr.CodeUnavailable, true
	case remote == "internalerror":
		code = speecherr.CodeInternal
	default:
		// Unrecognized vendor codes may still carry a useful diagnostic.
		// Never infer a retry merely from a message or an unknown code.
		desc := strings.ToLower(h.ErrorMessage)
		switch {
		case strings.Contains(desc, "authentication") || strings.Contains(desc, "permission denied"):
			code = speecherr.CodeUnauthorized
		case strings.Contains(desc, "throttl") || strings.Contains(desc, "rate limit") ||
			strings.Contains(desc, "quota"):
			code = speecherr.CodeRateLimited
		case strings.Contains(desc, "timeout"):
			code = speecherr.CodeTimeout
		case strings.Contains(desc, "unavailable"):
			code = speecherr.CodeUnavailable
		}
	}
	return &speecherr.Error{
		Code: code, Provider: "aliyun", Operation: "task",
		RemoteCode: h.ErrorCode,
		Err:        fmt.Errorf("remote error %q: %s", h.ErrorCode, h.ErrorMessage),
		Retryable:  retryable,
	}
}
func (s *Stream) awaitStarted(ctx context.Context) error {
	typ, data, err := s.conn.ReadMessage()
	if err != nil {
		return wrapNetwork(ctx, "task-started", err)
	}
	if typ != websocket.TextMessage {
		return protocolError("task-started", errors.New("non-text ASR start response"))
	}
	ev, err := decodeEvent(data)
	if err != nil {
		return protocolError("task-started", err)
	}
	if ev.Header.TaskID != s.taskID {
		return protocolError("task-started", errors.New("task_id mismatch"))
	}
	switch ev.Header.Event {
	case "task-started":
		return nil
	case "task-failed":
		return taskFailure(ev.Header)
	default:
		return protocolError("task-started", fmt.Errorf("unexpected event %q", ev.Header.Event))
	}
}
