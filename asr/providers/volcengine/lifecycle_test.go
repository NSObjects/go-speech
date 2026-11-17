package volcengine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/gorilla/websocket"
)

// A per-Recv timeout must tear down its session and keep the first terminal
// context error available to later callers, without affecting other sessions.
func TestRecvDeadlineStopsSessionAndRetainsError(t *testing.T) {
	endpoint := mockEndpoint(t, func(ws *websocket.Conn, _ *http.Request) {
		_, _, _ = ws.ReadMessage() // initial config
		_, _, _ = ws.ReadMessage() // waits for the cancellation-driven close
	})
	client, err := New(Config{APIKey: "key", Endpoint: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	parent, cancelParent := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelParent()
	s, err := client.OpenStream(parent, asr.Config{
		Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	deadlineCtx, cancel := context.WithTimeout(parent, 100*time.Millisecond)
	defer cancel()
	_, first := s.Recv(deadlineCtx)
	if !errors.Is(first, context.DeadlineExceeded) {
		t.Fatalf("Recv deadline: got %v, want context deadline exceeded", first)
	}
	_, second := s.Recv(context.Background())
	if second != first {
		t.Fatalf("first terminal error changed: first=%v, second=%v", first, second)
	}
	if err := s.Send(parent, []byte{0, 0}); !errors.Is(err, asr.ErrStreamClosed) {
		t.Fatalf("Send after timed-out Recv: %v; want closed session", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

// A single Client may be shared by multiple independent connections. The
// response belonging to one socket must never be delivered to another caller.
func TestConcurrentIndependentStreams(t *testing.T) {
	const sessions = 4
	requestIDs := make(chan string, sessions)
	endpoint := mockEndpoint(t, func(ws *websocket.Conn, r *http.Request) {
		requestIDs <- r.Header.Get("X-Api-Request-Id")
		if _, _, err := ws.ReadMessage(); err != nil {
			t.Errorf("read config: %v", err)
			return
		}
		_, audioFrame, err := ws.ReadMessage()
		if err != nil {
			t.Errorf("read audio: %v", err)
			return
		}
		kind, flags, data, err := testRequestFrame(audioFrame)
		if err != nil || kind != messageAudioRequest || flags != 0 || len(data) != 2 {
			t.Errorf("invalid audio frame: kind=%d flags=%d bytes=%d err=%v", kind, flags, len(data), err)
			return
		}
		_, finalFrame, err := ws.ReadMessage()
		if err != nil {
			t.Errorf("read CloseSend: %v", err)
			return
		}
		kind, flags, _, err = testRequestFrame(finalFrame)
		if err != nil || kind != messageAudioRequest || flags != flagLast {
			t.Errorf("missing final marker: kind=%d flags=%d err=%v", kind, flags, err)
			return
		}
		value := strconv.Itoa(int(data[0]))
		serverResult(t, ws, true, map[string]any{
			"code": 1000,
			"result": map[string]any{
				"text": value,
				"utterances": []any{
					map[string]any{"text": value, "definite": true, "start_time": 0, "end_time": 100},
				},
			},
		})
	})
	client, err := New(Config{APIKey: "key", Endpoint: endpoint, ResultType: ResultSingle})
	if err != nil {
		t.Fatal(err)
	}
	outcomes := make(chan error, sessions)
	for i := 1; i <= sessions; i++ {
		go func(value int) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			stream, err := client.OpenStream(ctx, asr.Config{
				Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
			})
			if err != nil {
				outcomes <- fmt.Errorf("session %d open: %w", value, err)
				return
			}
			defer stream.Close()
			if err := stream.Send(ctx, []byte{byte(value), 0}); err != nil {
				outcomes <- fmt.Errorf("session %d send: %w", value, err)
				return
			}
			if err := stream.CloseSend(ctx); err != nil {
				outcomes <- fmt.Errorf("session %d CloseSend: %w", value, err)
				return
			}
			event, err := stream.Recv(ctx)
			if err != nil || event.Type != asr.EventFinal || event.Text != strconv.Itoa(value) {
				outcomes <- fmt.Errorf("session %d received wrong final: %#v err=%v", value, event, err)
				return
			}
			_, err = stream.Recv(ctx)
			if !errors.Is(err, io.EOF) {
				outcomes <- fmt.Errorf("session %d received %v; want EOF", value, err)
				return
			}
			outcomes <- nil
		}(i)
	}
	for i := 0; i < sessions; i++ {
		select {
		case err := <-outcomes:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(7 * time.Second):
			t.Fatal("concurrent sessions did not terminate")
		}
	}
	seen := make(map[string]bool, sessions)
	for i := 0; i < sessions; i++ {
		select {
		case id := <-requestIDs:
			if id == "" || seen[id] {
				t.Errorf("missing or repeated connection request ID %q", id)
			}
			seen[id] = true
		default:
			t.Error("missing connection request ID")
		}
	}
}
