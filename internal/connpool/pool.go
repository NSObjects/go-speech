// Package connpool manages exclusive leases of reusable WebSocket connections.
// Providers alone determine whether a completed protocol task is reusable.
package connpool

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/NSObjects/go-speech/transport"
	"github.com/gorilla/websocket"
)

var ErrClosed = errors.New("connection pool closed")

var ErrDisabled = errors.New("connection pool disabled")
var ErrInvalidWarmupCount = errors.New("warmup count must be between zero and MaxIdle")

type idleConn struct {
	conn  *websocket.Conn
	until time.Time
	timer *time.Timer
}

// Pool is owned by a single provider client and never shared across credentials.
type Pool struct {
	cfg     transport.PoolConfig
	mu      sync.Mutex
	closed  bool
	open    int
	born    map[*websocket.Conn]time.Time
	keys    map[*websocket.Conn]string
	idle    []*idleConn
	changed chan struct{}
	warming map[string]int
	pending int
}

func New(cfg *transport.PoolConfig) (*Pool, error) {
	if cfg == nil {
		return nil, nil
	}
	c := *cfg
	if c.MaxOpen < 0 || c.MaxIdle < 0 || c.IdleTimeout < 0 || c.MaxLifetime < 0 || c.AcquireTimeout < 0 {
		return nil, errors.New("negative connection pool setting")
	}
	if c.MaxOpen == 0 {
		c.MaxOpen = 16
	}
	if c.MaxIdle == 0 {
		c.MaxIdle = min(4, c.MaxOpen)
	}
	if c.MaxIdle > c.MaxOpen {
		return nil, errors.New("MaxIdle exceeds MaxOpen")
	}
	if c.IdleTimeout == 0 {
		c.IdleTimeout = 30 * time.Second
	}
	// DashScope closes idle connections at approximately 60s. Use a margin.
	if c.IdleTimeout > 45*time.Second {
		return nil, errors.New("IdleTimeout must not exceed 45s")
	}
	if c.MaxLifetime == 0 {
		c.MaxLifetime = 10 * time.Minute
	}
	return &Pool{cfg: c, born: make(map[*websocket.Conn]time.Time), keys: make(map[*websocket.Conn]string), warming: make(map[string]int), changed: make(chan struct{})}, nil
}

func (p *Pool) signal() {
	close(p.changed)
	p.changed = make(chan struct{})
}

// Acquire returns an exclusive connection; a nil pool dials directly.
// No task is retried if a reused connection was closed by the server.
func (p *Pool) Acquire(ctx context.Context, dial func(context.Context) (*websocket.Conn, error)) (*websocket.Conn, error) {
	return p.AcquireKey(ctx, "", dial)
}

// AcquireKey only leases sockets bound to the requested transport identity.
// Capacity is global, not per key; an idle unmatched socket can be evicted
// to serve a different key when all open slots are occupied.
func (p *Pool) AcquireKey(ctx context.Context, key string, dial func(context.Context) (*websocket.Conn, error)) (*websocket.Conn, error) {
	if p == nil {
		return dial(ctx)
	}
	if p.cfg.AcquireTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.cfg.AcquireTimeout)
		defer cancel()
	}
	for {
		p.mu.Lock()
		if err := ctx.Err(); err != nil {
			p.mu.Unlock()
			return nil, err
		}
		if p.closed {
			p.mu.Unlock()
			return nil, ErrClosed
		}
		for i := len(p.idle) - 1; i >= 0; i-- {
			e := p.idle[i]
			if !time.Now().Before(e.until) {
				p.removeIdle(i)
				p.drop(e.conn)
				continue
			}
			if p.keys[e.conn] != key {
				continue
			}
			p.removeIdle(i)
			p.mu.Unlock()
			return e.conn, nil
		}
		if p.open >= p.cfg.MaxOpen && len(p.idle) > 0 {
			e := p.idle[0]
			p.removeIdle(0)
			p.drop(e.conn)
		}
		if p.open < p.cfg.MaxOpen {
			p.open++
			p.mu.Unlock()
			conn, err := dial(ctx)
			p.mu.Lock()
			if err == nil && ctx.Err() != nil {
				err = ctx.Err()
			}
			if err == nil && p.closed {
				err = ErrClosed
			}
			if err != nil {
				// Keep the reservation until a late successful dial has
				// actually closed. Otherwise another acquisition could
				// transiently exceed MaxOpen physical sockets.
				p.mu.Unlock()
				if conn != nil {
					_ = conn.Close()
				}
				p.mu.Lock()
				p.open--
				p.signal()
				p.mu.Unlock()
				return nil, err
			}
			p.born[conn] = time.Now()
			p.keys[conn] = key
			p.mu.Unlock()
			return conn, nil
		}
		ch := p.changed
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ch:
		}
	}
}

// WarmupKey makes a bounded, synchronous best-effort attempt to reach count
// idle connections for key. It never waits for capacity, evicts another key,
// sends protocol tasks, or retries failed dials. Concurrent warmups reserve
// capacity before dialing. Foreground requests may consume warmed sockets.
func (p *Pool) WarmupKey(ctx context.Context, key string, count int, dial func(context.Context) (*websocket.Conn, error)) error {
	if p == nil {
		return ErrDisabled
	}
	if count < 0 || count > p.cfg.MaxIdle {
		return ErrInvalidWarmupCount
	}
	if p.cfg.AcquireTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.cfg.AcquireTimeout)
		defer cancel()
	}
	for attempt := 0; attempt < count; attempt++ {
		p.mu.Lock()
		if err := ctx.Err(); err != nil {
			p.mu.Unlock()
			return err
		}
		if p.closed {
			p.mu.Unlock()
			return ErrClosed
		}
		matching := p.warming[key]
		for i := len(p.idle) - 1; i >= 0; i-- {
			e := p.idle[i]
			if !time.Now().Before(e.until) {
				p.removeIdle(i)
				p.drop(e.conn)
				continue
			}
			if p.keys[e.conn] == key {
				matching++
			}
		}
		if matching >= count || p.open >= p.cfg.MaxOpen || len(p.idle)+p.pending >= p.cfg.MaxIdle {
			p.mu.Unlock()
			return nil
		}
		p.open++
		p.pending++
		p.warming[key]++
		p.mu.Unlock()

		conn, err := dial(ctx)
		p.mu.Lock()
		p.pending--
		p.warming[key]--
		if p.warming[key] == 0 {
			delete(p.warming, key)
		}
		if err == nil {
			err = ctx.Err()
		}
		if err == nil && p.closed {
			err = ErrClosed
		}
		if err != nil {
			p.mu.Unlock()
			if conn != nil {
				_ = conn.Close()
			}
			p.mu.Lock()
			p.open--
			p.signal()
			p.mu.Unlock()
			return err
		}
		p.born[conn] = time.Now()
		p.keys[conn] = key
		p.releaseLocked(conn, true)
		p.mu.Unlock()
	}
	return nil
}

// removeIdle removes an idle entry and stops its timer while holding mu.
func (p *Pool) removeIdle(i int) {
	p.idle[i].timer.Stop()
	copy(p.idle[i:], p.idle[i+1:])
	p.idle[len(p.idle)-1] = nil
	p.idle = p.idle[:len(p.idle)-1]
}

// drop removes a tracked connection while holding mu.
func (p *Pool) drop(conn *websocket.Conn) {
	delete(p.born, conn)
	delete(p.keys, conn)
	p.open--
	_ = conn.Close()
	p.signal()
}

// Release must be called exactly once by the owning stream after all socket
// operations have stopped, except for discard which may interrupt pending I/O.
func (p *Pool) Release(conn *websocket.Conn, reusable bool) {
	if p == nil {
		_ = conn.Close()
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.releaseLocked(conn, reusable)
}

func (p *Pool) releaseLocked(conn *websocket.Conn, reusable bool) {
	born, ok := p.born[conn]
	if !ok {
		return
	}
	now := time.Now()
	if !reusable || p.closed || len(p.idle) >= p.cfg.MaxIdle || !now.Before(born.Add(p.cfg.MaxLifetime)) {
		p.drop(conn)
		return
	}
	until := now.Add(p.cfg.IdleTimeout)
	if end := born.Add(p.cfg.MaxLifetime); end.Before(until) {
		until = end
	}
	e := &idleConn{conn: conn, until: until}
	e.timer = time.AfterFunc(time.Until(until), func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		for i, current := range p.idle {
			if current == e {
				copy(p.idle[i:], p.idle[i+1:])
				p.idle[len(p.idle)-1] = nil
				p.idle = p.idle[:len(p.idle)-1]
				p.drop(conn)
				return
			}
		}
	})
	p.idle = append(p.idle, e)
	p.signal()
}

// Close rejects future acquisitions and closes idle sockets. Active tasks
// may finish; their sockets are discarded on return. It is idempotent.
func (p *Pool) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	for _, e := range p.idle {
		e.timer.Stop()
		p.drop(e.conn)
	}
	p.idle = nil
	p.signal()
	return nil
}
