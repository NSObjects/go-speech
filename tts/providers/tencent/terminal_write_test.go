package tencent

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/tts"
	"github.com/gorilla/websocket"
)

var injectedWriteFailure = errors.New("injected write failure")

type failingWriteConn struct {
	net.Conn
	fail *atomic.Bool
}

func (c *failingWriteConn) Write(p []byte) (int, error) {
	if c.fail.Load() {
		return 0, injectedWriteFailure
	}
	return c.Conn.Write(p)
}

func TestDuplexWriteFailureRetainedByConcurrentRecv(t *testing.T) {
	var fail atomic.Bool
	dialer := &websocket.Dialer{NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return &failingWriteConn{Conn: conn, fail: &fail}, nil
	}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer ws.Close()
		_ = ws.WriteJSON(map[string]any{"code": 0, "session_id": r.URL.Query().Get("SessionId"), "ready": 1})
		_, _, _ = ws.ReadMessage()
	}))
	defer server.Close()
	c, err := New(Config{AppID: "1", SecretID: "id", SecretKey: "key", Endpoint: "ws" + strings.TrimPrefix(server.URL, "http") + "/stream_wsv2", Dialer: dialer})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := c.OpenDuplex(ctx, tts.DuplexRequest{Voice: "101001"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	received := make(chan error, 1)
	go func() {
		_, err := s.Recv(ctx)
		received <- err
	}()
	fail.Store(true)
	first := s.SendText(ctx, "hello")
	if !errors.Is(first, injectedWriteFailure) {
		t.Fatalf("write error = %v", first)
	}
	select {
	case got := <-received:
		if got != first {
			t.Fatalf("reader lost original write error: got %v, want %v", got, first)
		}
	case <-ctx.Done():
		t.Fatal("reader did not unblock after write failure")
	}
	for range 2 {
		if _, err := s.Recv(context.Background()); err != first {
			t.Fatalf("terminal error changed: %v", err)
		}
	}
}
