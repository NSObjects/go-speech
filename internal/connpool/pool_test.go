package connpool

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/transport"
	"github.com/gorilla/websocket"
)

func TestPoolCapacityExpiryAndClose(t *testing.T) {
	closed := make(chan struct{}, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		_, _, _ = ws.ReadMessage()
		closed <- struct{}{}
	}))
	defer server.Close()
	dial := func(ctx context.Context) (*websocket.Conn, error) {
		conn, _, err := websocket.DefaultDialer.DialContext(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
		return conn, err
	}
	p, err := New(&transport.PoolConfig{MaxOpen: 1, IdleTimeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := p.Acquire(ctx, dial)
	if err != nil {
		t.Fatal(err)
	}
	wait, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	defer stop()
	if _, err := p.Acquire(wait, dial); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("capacity deadline: %v", err)
	}
	p.Release(conn, true)
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("idle socket not closed")
	}
	next, err := p.Acquire(ctx, dial)
	if err != nil {
		t.Fatal(err)
	}
	if next == conn {
		t.Fatal("expired socket reused")
	}
	p.Release(next, true)
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Acquire(ctx, dial); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed pool: %v", err)
	}
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("Close did not close idle socket")
	}
}
