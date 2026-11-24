package volcengine

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/NSObjects/go-speech/tts"
	"github.com/gorilla/websocket"
)

const (
	duplexStartConnection   uint32 = 1
	duplexFinishSession     uint32 = 102
	duplexStartSession      uint32 = 100
	duplexTaskRequest       uint32 = 200
	duplexConnectionStarted uint32 = 50
	duplexSessionStarted    uint32 = 150
)

var _ tts.DuplexSynthesizer = (*Client)(nil)
var _ tts.DuplexStream = (*duplexStream)(nil)

// OpenDuplex opens the V3 bidirectional protocol, which is distinct from the
// unidirectional endpoint used by Synthesize and SynthesizeStream.
func (c *Client) OpenDuplex(ctx context.Context, cfg tts.DuplexRequest) (tts.DuplexStream, error) {
	if c == nil {
		return nil, invalid("nil client")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	req, encoding, err := validateRequest(tts.Request{Text: "validation", Voice: cfg.Voice, Format: cfg.Format})
	if err != nil {
		return nil, err
	}
	id, err := newID()
	if err != nil {
		return nil, &speecherr.Error{Code: speecherr.CodeInternal, Provider: "volcengine", Operation: "id", Err: err}
	}
	endpoint, err := url.Parse(c.cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	endpoint.Path = "/api/v3/tts/bidirection"
	dialer := *websocket.DefaultDialer
	if c.cfg.Dialer != nil {
		dialer = *c.cfg.Dialer
	}
	if dialer.HandshakeTimeout == 0 {
		dialer.HandshakeTimeout = c.cfg.StartTimeout
	}
	headers := http.Header{}
	headers.Set("X-Api-Key", c.cfg.APIKey)
	headers.Set("X-Api-Resource-Id", c.cfg.ResourceID)
	headers.Set("X-Api-Connect-Id", id)
	conn, response, err := dialer.DialContext(ctx, endpoint.String(), headers)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, dialFailure(err, response)
	}
	conn.SetReadLimit(maxWirePayload + 256)
	s := &duplexStream{
		conn: conn, parent: ctx, sessionID: id, voice: req.Voice,
		encoding: encoding, format: req.Format, done: make(chan struct{}),
	}
	go func() {
		select {
		case <-ctx.Done():
			_ = s.Close()
		case <-s.done:
		}
	}()
	if err := s.initialize(ctx, c.cfg.StartTimeout); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

type duplexStream struct {
	conn        *websocket.Conn
	parent      context.Context
	sessionID   string
	voice       string
	encoding    string
	format      audio.Format
	done        chan struct{}
	once        sync.Once
	readMu      sync.Mutex
	writeMu     sync.Mutex
	inputClosed bool
	textSent    bool
	finished    bool
	audioSeen   bool
	terminalErr error
}

func (s *duplexStream) Close() error {
	if s == nil {
		return nil
	}
	s.once.Do(func() { close(s.done); _ = s.conn.Close() })
	return nil
}
func (s *duplexStream) closed() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}
func (s *duplexStream) closeError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.parent.Err(); err != nil {
		return err
	}
	return tts.ErrStreamClosed
}
func (s *duplexStream) fail(err error) error {
	if s.terminalErr == nil {
		s.terminalErr = err
	}
	_ = s.Close()
	return s.terminalErr
}
func (s *duplexStream) networkError(ctx context.Context, op string, err error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if e := s.parent.Err(); e != nil {
		return e
	}
	var ne net.Error
	if d, ok := ctx.Deadline(); ok && !time.Now().Before(d) && errors.As(err, &ne) && ne.Timeout() {
		return context.DeadlineExceeded
	}
	if s.closed() {
		return tts.ErrStreamClosed
	}
	code := speecherr.CodeUnavailable
	if errors.As(err, &ne) && ne.Timeout() {
		code = speecherr.CodeTimeout
	}
	return &speecherr.Error{Code: code, Provider: "volcengine", Operation: op, Err: err, Retryable: true}
}

// encodeDuplexEvent uses the V3 full-client request envelope with an event ID
// and, for session events, a session identifier.
func encodeDuplexEvent(event uint32, session string, body []byte) ([]byte, error) {
	if len(body) > maxWirePayload {
		return nil, protocol("duplex request exceeds frame limit")
	}
	size := 12 + len(body)
	if session != "" {
		size += 4 + len(session)
	}
	frame := make([]byte, size)
	frame[0], frame[1], frame[2] = 0x11, 0x14, 0x10
	binary.BigEndian.PutUint32(frame[4:8], event)
	offset := 8
	if session != "" {
		binary.BigEndian.PutUint32(frame[offset:offset+4], uint32(len(session)))
		offset += 4
		copy(frame[offset:], session)
		offset += len(session)
	}
	binary.BigEndian.PutUint32(frame[offset:offset+4], uint32(len(body)))
	copy(frame[offset+4:], body)
	return frame, nil
}

func (s *duplexStream) send(ctx context.Context, event uint32, body []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.parent.Err(); err != nil {
		return err
	}
	if s.closed() {
		return s.closeError(ctx)
	}
	session := s.sessionID
	if event == duplexStartConnection {
		session = ""
	}
	frame, err := encodeDuplexEvent(event, session, body)
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
	defer stop()
	deadline := time.Now().Add(10 * time.Second)
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

func (s *duplexStream) receive(ctx context.Context, deadline time.Time) (wireFrame, error) {
	if err := ctx.Err(); err != nil {
		return wireFrame{}, err
	}
	if err := s.parent.Err(); err != nil {
		return wireFrame{}, err
	}
	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
	defer stop()
	if err := s.conn.SetReadDeadline(deadline); err != nil {
		return wireFrame{}, s.networkError(ctx, "recv", err)
	}
	typ, data, err := s.conn.ReadMessage()
	if err != nil {
		return wireFrame{}, s.networkError(ctx, "recv", err)
	}
	if typ != websocket.BinaryMessage {
		return wireFrame{}, protocol("expected binary duplex response")
	}
	// ConnectionStarted has no session_id; other response events are session-scoped.
	if len(data) >= 12 && binary.BigEndian.Uint32(data[4:8]) == duplexConnectionStarted {
		if data[0] != 0x11 || data[1]>>4 != kindFullResponse || data[1]&15 != 4 ||
			data[2]>>4 != 1 || data[2]&15 > 1 || data[3] != 0 {
			return wireFrame{}, protocol("invalid ConnectionStarted header")
		}
		size := binary.BigEndian.Uint32(data[8:12])
		if size > maxWirePayload || int(size) != len(data)-12 {
			return wireFrame{}, protocol("invalid ConnectionStarted payload")
		}
		payload := data[12:]
		if data[2]&15 == 1 {
			reader, err := gzip.NewReader(bytes.NewReader(payload))
			if err != nil {
				return wireFrame{}, protocol("invalid compressed ConnectionStarted")
			}
			decoded, err := io.ReadAll(io.LimitReader(reader, maxWirePayload+1))
			_ = reader.Close()
			if err != nil || len(decoded) > maxWirePayload {
				return wireFrame{}, protocol("oversized compressed ConnectionStarted")
			}
			payload = decoded
		}
		return wireFrame{kind: kindFullResponse, event: duplexConnectionStarted, payload: payload}, nil
	}
	return parseResponse(data)
}

func (s *duplexStream) initialize(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := s.send(ctx, duplexStartConnection, nil); err != nil {
		return err
	}
	frame, err := s.receive(ctx, deadline)
	if err != nil {
		return err
	}
	if frame.kind == kindServerError {
		return frame.failure()
	}
	if frame.event != duplexConnectionStarted {
		return protocol("expected ConnectionStarted")
	}
	body, err := json.Marshal(map[string]any{
		"event": duplexStartSession,
		"req_params": map[string]any{
			"speaker":      s.voice,
			"audio_params": map[string]any{"format": s.encoding, "sample_rate": s.format.SampleRate},
		},
	})
	if err != nil {
		return err
	}
	if err := s.send(ctx, duplexStartSession, body); err != nil {
		return err
	}
	frame, err = s.receive(ctx, deadline)
	if err != nil {
		return err
	}
	if frame.kind == kindServerError {
		return frame.failure()
	}
	if frame.event != duplexSessionStarted || frame.session != s.sessionID {
		return protocol("expected matching SessionStarted")
	}
	return nil
}

func (s *duplexStream) SendText(ctx context.Context, text string) error {
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
	body, err := json.Marshal(map[string]any{
		"event": duplexTaskRequest,
		"req_params": map[string]any{
			"text":         text,
			"speaker":      s.voice,
			"audio_params": map[string]any{"format": s.encoding, "sample_rate": s.format.SampleRate},
		},
	})
	if err != nil {
		return err
	}
	if err := s.send(ctx, duplexTaskRequest, body); err != nil {
		_ = s.Close()
		return err
	}
	s.textSent = true
	return nil
}
func (s *duplexStream) CloseSend(ctx context.Context) error {
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
	if err := s.send(ctx, duplexFinishSession, nil); err != nil {
		_ = s.Close()
		return err
	}
	s.inputClosed = true
	return nil
}

func (s *duplexStream) Recv(ctx context.Context) (audio.Chunk, error) {
	if s == nil {
		return audio.Chunk{}, tts.ErrStreamClosed
	}
	s.readMu.Lock()
	defer s.readMu.Unlock()
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
		var deadline time.Time
		if d, ok := ctx.Deadline(); ok {
			deadline = d
		}
		frame, err := s.receive(ctx, deadline)
		if err != nil {
			return audio.Chunk{}, s.fail(err)
		}
		if frame.kind == kindServerError {
			return audio.Chunk{}, s.fail(frame.failure())
		}
		if frame.session != s.sessionID {
			return audio.Chunk{}, s.fail(protocol("mismatched duplex session ID"))
		}
		switch frame.event {
		case eventSessionFinished:
			if frame.kind != kindFullResponse {
				return audio.Chunk{}, s.fail(protocol("invalid SessionFinished response"))
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
		case eventTTSAudio:
			if frame.kind != kindAudioResponse {
				return audio.Chunk{}, s.fail(protocol("invalid TTS audio response"))
			}
			if len(frame.payload) > 0 {
				s.audioSeen = true
				return audio.Chunk{Data: frame.payload, Format: s.format}, nil
			}
		default:
			if frame.kind == kindAudioResponse {
				return audio.Chunk{}, s.fail(protocol("unexpected duplex audio response"))
			}
		}
	}
}
