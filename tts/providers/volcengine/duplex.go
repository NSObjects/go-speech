package volcengine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/NSObjects/go-speech/speecherr"
	"github.com/NSObjects/go-speech/tts"
	"github.com/gorilla/websocket"
)

type clientEvent struct {
	Event         string         `json:"event"`
	SessionID     string         `json:"session_id,omitempty"`
	ModelID       string         `json:"model_id,omitempty"`
	LanguageCode  string         `json:"language_code,omitempty"`
	OutputFormat  *outputFormat  `json:"output_format,omitempty"`
	Content       string         `json:"content,omitempty"`
	VoiceSettings *voiceSettings `json:"voice_settings,omitempty"`
	advancedRequest
}

type duplex struct {
	*stream
	conn        *websocket.Conn
	writeMu     sync.Mutex
	inputClosed bool
	textRunes   int
}

var (
	_ tts.DuplexStream = (*duplex)(nil)
	_ tts.Flusher      = (*duplex)(nil)
)

// OpenDuplex opens the JSON text-event protocol and waits for ready before
// accepting incremental text. CloseSend submits the remaining text; Close
// cancels the entire session, including any outstanding audio receive.
func (c *Client) OpenDuplex(ctx context.Context, req tts.DuplexRequest) (tts.DuplexStream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	format, init, err := c.prepareDuplex(req)
	if err != nil {
		return nil, err
	}
	id := init.SessionID
	if id == "" {
		var idBytes [16]byte
		if _, err := rand.Read(idBytes[:]); err != nil {
			return nil, err
		}
		id = hex.EncodeToString(idBytes[:])
	}
	startCtx, cancel := context.WithTimeout(ctx, c.cfg.StartTimeout)
	defer cancel()
	dialer := *websocket.DefaultDialer
	if c.cfg.Dialer != nil {
		dialer = *c.cfg.Dialer
	}
	header := http.Header{"X-Api-Key": []string{c.cfg.APIKey}}
	conn, response, err := dialer.DialContext(startCtx, c.endpoint(req.Voice, "streaming-input", true), header)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if startCtx.Err() != nil {
			return nil, startFailure(ctx, startCtx, "connect", err)
		}
		if response != nil {
			return nil, httpFailure(response.StatusCode)
		}
		return nil, startFailure(ctx, startCtx, "connect", err)
	}
	conn.SetReadLimit(maxEventBytes)
	next := func() (event, error) {
		typ, raw, err := conn.ReadMessage()
		if err != nil {
			return event{}, failure(speecherr.CodeUnavailable, "receive", err)
		}
		if typ != websocket.TextMessage {
			return event{}, protocol("expected JSON text frame")
		}
		return decodeEvent(raw)
	}
	d := &duplex{conn: conn}
	d.stream = newStream(ctx, format, next, func() { _ = conn.Close() })
	init.SessionID = id
	if err := d.initialize(ctx, startCtx, init); err != nil {
		_ = d.Close()
		return nil, err
	}
	return d, nil
}

// startFailure distinguishes a caller cancellation from the SDK's startup
// budget and identifies the failing protocol phase without exposing payloads.
// It never retries an attempted connection or initialization.
func startFailure(parent, startup context.Context, phase string, err error) error {
	if cause := parent.Err(); cause != nil {
		return cause
	}
	if cause := startup.Err(); cause != nil {
		if errors.Is(cause, context.DeadlineExceeded) {
			return failure(speecherr.CodeTimeout, phase, cause)
		}
		return cause
	}
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &ne) && ne.Timeout() {
		return failure(speecherr.CodeTimeout, phase, err)
	}
	return failure(speecherr.CodeUnavailable, phase, err)
}

func (d *duplex) initialize(parent, ctx context.Context, init clientEvent) error {
	stop := context.AfterFunc(ctx, func() { _ = d.terminate(ctx.Err()) })
	defer stop()
	if err := d.send(ctx, init); err != nil {
		return startFailure(parent, ctx, "init_send", err)
	}
	e, err := d.next()
	if ctx.Err() != nil || err != nil {
		var classified *speecherr.Error
		if ctx.Err() == nil && errors.As(err, &classified) && classified.Code == speecherr.CodeProtocol {
			return err
		}
		return startFailure(parent, ctx, "await_ready", err)
	}
	if e.Event == "error" {
		return e.failure()
	}
	if e.Event != "ready" || e.SessionID != init.SessionID {
		return protocol("expected matching ready")
	}
	d.ready = true
	return nil
}

func (d *duplex) send(ctx context.Context, e clientEvent) error {
	if err := d.contextError(ctx); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = d.terminate(ctx.Err()) })
	defer stop()
	deadline := time.Now().Add(10 * time.Second)
	if limit, ok := ctx.Deadline(); ok && limit.Before(deadline) {
		deadline = limit
	}
	if err := d.conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	if err := d.conn.WriteJSON(e); err != nil {
		if contextErr := d.contextError(ctx); contextErr != nil {
			return contextErr
		}
		return failure(speecherr.CodeUnavailable, "send", err)
	}
	return nil
}

func (d *duplex) SendText(ctx context.Context, text string) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	if err := d.contextError(ctx); err != nil {
		return err
	}
	if d.inputClosed {
		return tts.ErrInputClosed
	}
	count := utf8.RuneCountInString(text)
	if !utf8.ValidString(text) || text == "" || d.textRunes+count > maxTextRunes {
		return invalid("invalid text fragment or session text limit exceeded")
	}
	if err := d.send(ctx, clientEvent{Event: "text", Content: text}); err != nil {
		return d.failWrite(err)
	}
	d.textRunes += count
	return nil
}

func (d *duplex) CloseSend(ctx context.Context) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	if err := d.contextError(ctx); err != nil {
		return err
	}
	if d.inputClosed {
		return tts.ErrInputClosed
	}
	if d.textRunes == 0 {
		return invalid("at least one text fragment required")
	}
	if err := d.send(ctx, clientEvent{Event: "close"}); err != nil {
		return d.failWrite(err)
	}
	d.inputClosed = true
	return nil
}

// Flush submits the current text batch while leaving the input stream open.
// The receiver continues through the intermediate FLUSHED event.
func (d *duplex) Flush(ctx context.Context) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	if err := d.contextError(ctx); err != nil {
		return err
	}
	if d.inputClosed {
		return tts.ErrInputClosed
	}
	if d.textRunes == 0 {
		return invalid("at least one text fragment required")
	}
	if err := d.send(ctx, clientEvent{Event: "flush"}); err != nil {
		return d.failWrite(err)
	}
	return nil
}
