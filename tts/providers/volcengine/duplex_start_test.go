package volcengine

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/speecherr"
	"github.com/NSObjects/go-speech/tts"
	"github.com/gorilla/websocket"
)

func TestDuplexStartupTimeoutPhase(t *testing.T) {
	t.Run("connect", func(t *testing.T) {
		var dials atomic.Int32
		c, err := New(Config{APIKey: fixtureKey, StartTimeout: 100 * time.Millisecond, Dialer: &websocket.Dialer{
			NetDialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				dials.Add(1)
				<-ctx.Done()
				return nil, ctx.Err()
			},
		}})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, err = c.OpenDuplex(ctx, tts.DuplexRequest{Voice: fixtureVoice})
		assertStartupTimeout(t, err, "seed3_connect")
		if dials.Load() != 1 {
			t.Fatalf("unexpected dial count: %d", dials.Load())
		}
		if ctx.Err() != nil {
			t.Fatal("startup timeout exhausted caller context")
		}
	})

	t.Run("await_ready", func(t *testing.T) {
		initReceived := make(chan struct{})
		socketClosed := make(chan struct{})
		c := fixtureClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			upgrader := websocket.Upgrader{}
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			defer close(socketClosed)
			_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			var init clientEvent
			if err := conn.ReadJSON(&init); err != nil || init.Event != "init" {
				t.Errorf("missing init: %v", err)
				return
			}
			close(initReceived)
			_, _, _ = conn.ReadMessage()
		}))
		c.cfg.StartTimeout = 300 * time.Millisecond
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, err := c.OpenDuplex(ctx, tts.DuplexRequest{Voice: fixtureVoice})
		assertStartupTimeout(t, err, "seed3_await_ready")
		select {
		case <-initReceived:
		default:
			t.Fatal("server never received init")
		}
		select {
		case <-socketClosed:
		case <-ctx.Done():
			t.Fatal("startup failure leaked socket")
		}
	})
}

func TestDuplexStartupPreservesCallerCancellation(t *testing.T) {
	started := make(chan struct{})
	c, err := New(Config{APIKey: fixtureKey, Dialer: &websocket.Dialer{
		NetDialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-started
		cancel()
	}()
	_, err = c.OpenDuplex(ctx, tts.DuplexRequest{Voice: fixtureVoice})
	if err != context.Canceled {
		t.Fatalf("caller cancellation changed: %v", err)
	}
}

func assertStartupTimeout(t *testing.T, err error, op string) {
	t.Helper()
	var e *speecherr.Error
	if !errors.Is(err, context.DeadlineExceeded) || !errors.As(err, &e) ||
		e.Code != speecherr.CodeTimeout || e.Operation != op || e.Retryable {
		t.Fatalf("unexpected startup error: %v", err)
	}
}
