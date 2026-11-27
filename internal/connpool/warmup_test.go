package connpool

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/transport"
	"github.com/gorilla/websocket"
)

func TestWarmupKeyTargetAndCapacity(t *testing.T) {
	var dials atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		_, _, _ = ws.ReadMessage()
	}))
	defer server.Close()
	p, err := New(&transport.PoolConfig{MaxOpen: 2, MaxIdle: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dial := func(ctx context.Context) (*websocket.Conn, error) {
		dials.Add(1)
		conn, _, err := websocket.DefaultDialer.DialContext(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
		return conn, err
	}
	warm, stop := context.WithCancel(ctx)
	if err := p.WarmupKey(warm, "a", 2, dial); err != nil {
		stop()
		t.Fatal(err)
	}
	stop()
	if err := p.WarmupKey(ctx, "a", 2, dial); err != nil {
		t.Fatal(err)
	}
	if err := p.WarmupKey(ctx, "b", 2, dial); err != nil {
		t.Fatal(err)
	}
	if dials.Load() != 2 {
		t.Fatalf("repeated or different-key warmup dialed %d sockets", dials.Load())
	}
	first, err := p.AcquireKey(ctx, "a", dial)
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.AcquireKey(ctx, "a", dial)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || dials.Load() != 2 {
		t.Fatal("warm sockets not independently leased")
	}
	if err := p.WarmupKey(ctx, "a", 2, dial); err != nil {
		t.Fatal(err)
	}
	if dials.Load() != 2 {
		t.Fatal("warmup exceeded active capacity")
	}
	p.Release(first, false)
	p.Release(second, false)
	if err := p.WarmupKey(ctx, "b", 1, dial); err != nil {
		t.Fatal(err)
	}
	if dials.Load() != 3 {
		t.Fatal("released capacity was not used")
	}
}

func TestWarmupFailureAndConcurrentReservation(t *testing.T) {
	p, err := New(&transport.PoolConfig{MaxOpen: 2, MaxIdle: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	want := errors.New("dial failure")
	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	dial := func(ctx context.Context) (*websocket.Conn, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
			return nil, want
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	done := make(chan error, 1)
	go func() { done <- p.WarmupKey(ctx, "a", 1, dial) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := p.WarmupKey(ctx, "a", 1, dial); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate concurrent warmup")
	}
	// A separate immediate failure must be attempted only once.
	fail := func(context.Context) (*websocket.Conn, error) {
		calls.Add(1)
		return nil, want
	}
	if err := p.WarmupKey(ctx, "b", 2, fail); !errors.Is(err, want) {
		t.Fatalf("failure=%v", err)
	}
	if calls.Load() != 2 {
		t.Fatal("warmup retried a failed dial")
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("in-flight cancellation=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("warmup did not finish")
	}
	if err := p.WarmupKey(context.Background(), "a", 1, fail); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed pool=%v", err)
	}
	if err := p.WarmupKey(context.Background(), "a", 3, fail); !errors.Is(err, ErrInvalidWarmupCount) {
		t.Fatalf("invalid count=%v", err)
	}
	var disabled *Pool
	if err := disabled.WarmupKey(ctx, "", 1, fail); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled pool=%v", err)
	}
}
