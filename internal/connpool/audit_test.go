package connpool

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/transport"
	"github.com/gorilla/websocket"
)

func auditDialer(t *testing.T) (func(context.Context) (*websocket.Conn, error), <-chan struct{}) {
	t.Helper()
	closed := make(chan struct{}, 1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		_, _, _ = ws.ReadMessage()
		closed <- struct{}{}
	}))
	t.Cleanup(server.Close)
	return func(ctx context.Context) (*websocket.Conn, error) {
		ws, _, err := websocket.DefaultDialer.DialContext(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
		return ws, err
	}, closed
}

// Exercise competing keys, warmups, successful reuse and discard under
// the race detector. Assert the invariants, not just absence of a race.
func TestPoolConcurrentLeaseAudit(t *testing.T) {
	dial, _ := auditDialer(t)
	p, err := New(&transport.PoolConfig{MaxOpen: 4, MaxIdle: 2, IdleTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var mu sync.Mutex
	active := make(map[*websocket.Conn]bool)
	var wg sync.WaitGroup
	start := make(chan struct{})
	check := func(conn *websocket.Conn, key string) {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.open > p.cfg.MaxOpen || p.open < len(p.born) || len(p.idle) > p.cfg.MaxIdle ||
			len(p.born) != len(p.keys) || p.pending < 0 {
			t.Errorf("pool accounting: open=%d born=%d keys=%d idle=%d pending=%d", p.open, len(p.born), len(p.keys), len(p.idle), p.pending)
		}
		if conn != nil {
			if _, ok := p.born[conn]; !ok || p.keys[conn] != key {
				t.Error("untracked or mismatched lease")
			}
			for _, e := range p.idle {
				if e.conn == conn {
					t.Error("leased socket is also idle")
				}
			}
		}
	}
	keys := []string{"a", "b", "c"}
	for worker := range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := range 12 {
				key := keys[(worker+i)%len(keys)]
				conn, err := p.AcquireKey(ctx, key, dial)
				if err != nil {
					t.Errorf("acquire: %v", err)
					return
				}
				mu.Lock()
				if active[conn] {
					t.Error("socket leased simultaneously to two callers")
				}
				active[conn] = true
				mu.Unlock()
				check(conn, key)
				time.Sleep(time.Millisecond)
				mu.Lock()
				delete(active, conn)
				mu.Unlock()
				p.Release(conn, i%4 != 0)
				check(nil, "")
			}
		}()
	}
	for _, key := range keys {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range 12 {
				if err := p.WarmupKey(ctx, key, 2, dial); err != nil {
					t.Errorf("warmup: %v", err)
					return
				}
				check(nil, "")
				time.Sleep(time.Millisecond)
			}
		}()
	}
	close(start)
	wg.Wait()
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.open != 0 || len(p.born) != 0 || len(p.keys) != 0 || len(p.idle) != 0 ||
		p.pending != 0 || len(p.warming) != 0 {
		t.Fatalf("resources retained after close: open=%d born=%d keys=%d idle=%d pending=%d warming=%d",
			p.open, len(p.born), len(p.keys), len(p.idle), p.pending, len(p.warming))
	}
}

func TestPoolMaxLifetimeActiveAndIdleAudit(t *testing.T) {
	dial, closed := auditDialer(t)
	p, err := New(&transport.PoolConfig{MaxOpen: 1, IdleTimeout: time.Second, MaxLifetime: 80 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, err := p.Acquire(ctx, dial)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
		t.Fatal("MaxLifetime interrupted an active lease")
	case <-time.After(150 * time.Millisecond):
	}
	p.Release(first, true)
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("expired active socket was not closed on return")
	}
	next, err := p.Acquire(ctx, dial)
	if err != nil {
		t.Fatal(err)
	}
	if next == first {
		t.Fatal("expired socket reused")
	}
	p.Release(next, true)
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("idle MaxLifetime did not close socket")
	}
}

func TestPoolCloseDuringDialAudit(t *testing.T) {
	dial, closed := auditDialer(t)
	for _, warm := range []bool{false, true} {
		name := "acquire"
		if warm {
			name = "warmup"
		}
		t.Run(name, func(t *testing.T) {
			p, err := New(&transport.PoolConfig{MaxOpen: 1})
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			entered, proceed := make(chan struct{}), make(chan struct{})
			done := make(chan error, 1)
			block := func(ctx context.Context) (*websocket.Conn, error) {
				ws, err := dial(ctx)
				close(entered)
				if err != nil {
					return nil, err
				}
				select {
				case <-proceed:
					return ws, nil
				case <-ctx.Done():
					_ = ws.Close()
					return nil, ctx.Err()
				}
			}
			go func() {
				if warm {
					done <- p.WarmupKey(ctx, "a", 1, block)
					return
				}
				_, err := p.AcquireKey(ctx, "a", block)
				done <- err
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if err := p.Close(); err != nil {
				t.Fatal(err)
			}
			close(proceed)
			select {
			case err := <-done:
				if !errors.Is(err, ErrClosed) {
					t.Fatalf("dial completed after close: %v", err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			select {
			case <-closed:
			case <-ctx.Done():
				t.Fatal("late dial leaked socket")
			}
			p.mu.Lock()
			defer p.mu.Unlock()
			if p.open != 0 || p.pending != 0 || len(p.born) != 0 || len(p.warming) != 0 {
				t.Fatal("late dial leaked pool capacity")
			}
		})
	}
}
