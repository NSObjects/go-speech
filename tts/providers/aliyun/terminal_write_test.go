package aliyun

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
		var run struct {
			Header struct {
				TaskID string `json:"task_id"`
			} `json:"header"`
		}
		if err := ws.ReadJSON(&run); err != nil {
			t.Error(err)
			return
		}
		_ = ws.WriteJSON(map[string]any{"header": map[string]any{"event": "task-started", "task_id": run.Header.TaskID}})
		_, _, _ = ws.ReadMessage()
	}))
	defer server.Close()
	c, err := New(Config{APIKey: "test", Endpoint: "ws" + strings.TrimPrefix(server.URL, "http") + "/api-ws/v1/inference", Dialer: dialer})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := c.OpenDuplex(ctx, tts.DuplexRequest{Voice: "longanyang"})
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
