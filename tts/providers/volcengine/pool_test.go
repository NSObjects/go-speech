package volcengine

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/internal/connpool"
	"github.com/NSObjects/go-speech/transport"
	"github.com/NSObjects/go-speech/tts"
	"github.com/gorilla/websocket"
)

func TestDuplexPoolReuseAndVoiceIsolation(t *testing.T) {
	var dials atomic.Int32
	c := fixtureClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		dials.Add(1)
		_ = conn.SetReadDeadline(time.Now().Add(8 * time.Second))
		seen := make(map[string]bool)
		for {
			var init clientEvent
			if err := conn.ReadJSON(&init); err != nil {
				return
			}
			if init.Event != "init" || init.SessionID == "" || seen[init.SessionID] {
				t.Errorf("invalid or duplicate session: %+v", init)
				return
			}
			seen[init.SessionID] = true
			if err := conn.WriteJSON(event{Event: "ready", SessionID: init.SessionID}); err != nil {
				return
			}
			for {
				var in clientEvent
				if err := conn.ReadJSON(&in); err != nil {
					return
				}
				if in.Event != "close" {
					continue
				}
				// Distinct voice URLs produce distinct bytes.
				data := []byte{1, 2}
				if strings.Contains(r.URL.Path, "/second/") {
					data = []byte{3, 4}
				}
				if err := conn.WriteJSON(event{Event: "audio", Audio: data}); err != nil {
					return
				}
				if err := conn.WriteJSON(event{Event: "final", IsFinal: true, Reason: closedReason}); err != nil {
					return
				}
				break
			}
		}
	}))
	var err error
	c.pool, err = connpool.New(&transport.PoolConfig{MaxOpen: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	open := func(ctx context.Context, voice string) *duplex {
		t.Helper()
		s, err := c.OpenDuplex(ctx, tts.DuplexRequest{Voice: voice, Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 24000, Channels: 1}})
		if err != nil {
			t.Fatal(err)
		}
		return s.(*duplex)
	}
	finish := func(s *duplex, want string) {
		t.Helper()
		if err := s.SendText(ctx, "hello"); err != nil {
			t.Fatal(err)
		}
		if err := s.CloseSend(ctx); err != nil {
			t.Fatal(err)
		}
		a, err := s.Recv(ctx)
		if err != nil || string(a.Data) != want {
			t.Fatalf("audio=%v err=%v", a.Data, err)
		}
		for range 2 {
			if _, err := s.Recv(ctx); !errors.Is(err, io.EOF) {
				t.Fatalf("terminal=%v", err)
			}
		}
	}
	oldCtx, oldCancel := context.WithCancel(ctx)
	defer oldCancel()
	first := open(oldCtx, "first")
	finish(first, "\x01\x02")
	second := open(ctx, "first")
	if first.conn != second.conn || dials.Load() != 1 {
		t.Fatal("clean session not reused")
	}
	oldCancel()
	_ = first.Close()
	if err := first.SendText(ctx, "late"); !errors.Is(err, tts.ErrStreamClosed) && !errors.Is(err, context.Canceled) {
		t.Fatalf("old session write=%v", err)
	}
	finish(second, "\x01\x02")
	third := open(ctx, "second")
	if third.conn == second.conn || dials.Load() != 2 {
		t.Fatal("voice-specific endpoint was not isolated")
	}
	finish(third, "\x03\x04")
	aborted := open(ctx, "second")
	if aborted.conn != third.conn {
		t.Fatal("second voice connection not reused")
	}
	_ = aborted.Close()
	next := open(ctx, "second")
	if next.conn == aborted.conn || dials.Load() != 3 {
		t.Fatal("aborted connection reused")
	}
	finish(next, "\x03\x04")
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.OpenDuplex(ctx, tts.DuplexRequest{Voice: "second"}); !errors.Is(err, connpool.ErrClosed) {
		t.Fatalf("closed pool acquisition=%v", err)
	}
}
