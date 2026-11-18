package tencent

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/gorilla/websocket"
)

type injectedWriteConn struct {
	net.Conn
	blocked *atomic.Bool
	injected error
}

func (c *injectedWriteConn) Write(data []byte) (int, error) {
	if c.blocked.Load() {
		return 0, c.injected
	}
	return c.Conn.Write(data)
}

// Inject an error only AFTER a normal WebSocket handshake. This exercises
// the sending path without relying on platform-specific TCP teardown timing.
func clientWithInjectedWriteError(t *testing.T) (*Client, *atomic.Bool) {
	t.Helper()
	client := testClient(t, func(ws *websocket.Conn, req *http.Request) {
		if err := sendResponse(ws, req.URL.Query().Get("voice_id"), 0, 0, nil); err != nil {
			return
		}
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	})
	var blocked atomic.Bool
	dialer := &net.Dialer{}
	client.cfg.Dialer = &websocket.Dialer{
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := dialer.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			return &injectedWriteConn{
				Conn: conn, blocked: &blocked,
				injected: errors.New("injected write failure"),
			}, nil
		},
	}
	return client, &blocked
}

func TestTencentSendFailureIsTheStableReceiveFailure(t *testing.T) {
	client, blocked := clientWithInjectedWriteError(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.OpenStream(ctx, pcmConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	blocked.Store(true)
	sendErr := stream.Send(ctx, []byte{0, 0})
	if !speecherr.IsCode(sendErr, speecherr.CodeUnavailable) {
		t.Fatalf("want retryable send failure, got %v", sendErr)
	}
	for i := 0; i < 3; i++ {
		_, receiveErr := stream.Recv(context.Background())
		if receiveErr != sendErr {
			t.Fatalf("Recv %d lost original Send error: send=%v recv=%v", i, sendErr, receiveErr)
		}
	}
	if err := stream.Send(ctx, []byte{0, 0}); !errors.Is(err, asr.ErrStreamClosed) {
		t.Fatalf("closed stream must reject Send, got %v", err)
	}
}

func TestTencentCloseSendFailureIsTheStableReceiveFailure(t *testing.T) {
	client, blocked := clientWithInjectedWriteError(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.OpenStream(ctx, pcmConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err := stream.Send(ctx, []byte{0, 0}); err != nil {
		t.Fatal(err)
	}
	blocked.Store(true)
	finishErr := stream.CloseSend(ctx)
	if !speecherr.IsCode(finishErr, speecherr.CodeUnavailable) {
		t.Fatalf("want retryable finish failure, got %v", finishErr)
	}
	for i := 0; i < 3; i++ {
		_, receiveErr := stream.Recv(context.Background())
		if receiveErr != finishErr {
			t.Fatalf("Recv %d lost original CloseSend failure: finish=%v recv=%v", i, finishErr, receiveErr)
		}
	}
}

func TestTencentReceiveIdleTimeoutStable(t *testing.T) {
	client := testClient(t, func(ws *websocket.Conn, req *http.Request) {
		if err := sendResponse(ws, req.URL.Query().Get("voice_id"), 0, 0, nil); err != nil {
			return
		}
		// Deliberately silent peer: wait until the client releases the socket.
		_, _, _ = ws.ReadMessage()
	})
	client.cfg.ReadIdleTimeout = 80 * time.Millisecond
	stream, err := client.OpenStream(context.Background(), pcmConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	start := time.Now()
	_, idleErr := stream.Recv(context.Background())
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("ReadIdleTimeout ignored: elapsed=%s", elapsed)
	}
	if !speecherr.IsCode(idleErr, speecherr.CodeTimeout) {
		t.Fatalf("want typed read timeout, got %v", idleErr)
	}
	for i := 0; i < 3; i++ {
		_, again := stream.Recv(context.Background())
		if again != idleErr {
			t.Fatalf("Recv %d replaced terminal idle failure: %v / %v", i, idleErr, again)
		}
	}
}

func TestTencentReceiveIdleTimeoutResetsAfterMessages(t *testing.T) {
	client := testClient(t, func(ws *websocket.Conn, req *http.Request) {
		id := req.URL.Query().Get("voice_id")
		if err := sendResponse(ws, id, 0, 0, nil); err != nil {
			return
		}
		for i := 0; i < 3; i++ {
			time.Sleep(45 * time.Millisecond)
			if err := sendResponse(ws, id, 0, 0, &segment{
				SliceType: 1, Index: 0, Text: "正在识别",
			}); err != nil {
				return
			}
		}
		_ = sendResponse(ws, id, 0, 1, &segment{SliceType: 2, Index: 0, Text: "识别完成"})
	})
	client.cfg.ReadIdleTimeout = 250 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := client.OpenStream(ctx, pcmConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	for i := 0; i < 3; i++ {
		ev, err := stream.Recv(ctx)
		if err != nil || ev.Type != asr.EventPartial {
			t.Fatalf("partial %d: %+v / %v", i, ev, err)
		}
	}
	ev, err := stream.Recv(ctx)
	if err != nil || ev.Type != asr.EventFinal || ev.Text != "识别完成" {
		t.Fatalf("final after partials: %+v / %v", ev, err)
	}
	if _, err := stream.Recv(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("unexpected EOF result: %v", err)
	}
}

func TestTencentRejectsNegativeReadIdleTimeout(t *testing.T) {
	_, err := New(Config{
		AppID: "1250000000", SecretID: "id", SecretKey: "key",
		ReadIdleTimeout: -time.Second,
	})
	if !speecherr.IsCode(err, speecherr.CodeInvalidArgument) {
		t.Fatalf("negative ReadIdleTimeout must be invalid: %v", err)
	}
}
