package volcengine

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/gorilla/websocket"
)

// A response with two definite utterances leaves one event queued after the
// first Recv. Canceled/closed active streams must not deliver that backlog,
// but a provider-finished stream must drain it and then return stable EOF.
func TestPendingVolcengineEventsRespectStreamLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name string
		last bool
		action string
	}{
		{name: "canceled Recv discards active backlog", action: "cancel"},
		{name: "explicit Close discards active backlog", action: "close"},
		{name: "provider final drains backlog", last: true, action: "finished"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := mockEndpoint(t, func(ws *websocket.Conn, _ *http.Request) {
				if _, _, err := ws.ReadMessage(); err != nil {
					return // Initial full request.
				}
				if _, _, err := ws.ReadMessage(); err != nil {
					return // First audio frame.
				}
				serverResult(t, ws, tc.last, map[string]any{
					"code": 1000,
					"result": map[string]any{
						"text": "第一句第二句",
						"utterances": []map[string]any{
							{"text": "第一句", "definite": true, "start_time": 0, "end_time": 800},
							{"text": "第二句", "definite": true, "start_time": 900, "end_time": 1600},
						},
					},
				})
				if !tc.last {
					_, _, _ = ws.ReadMessage() // Keep active session until shutdown.
				}
			})
			client, err := New(Config{APIKey: "test-key", Endpoint: endpoint})
			if err != nil {
				t.Fatal(err)
			}
			ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
			defer stop()
			session, err := client.OpenStream(ctx, asr.Config{
				Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			if err := session.Send(ctx, []byte{0, 0}); err != nil {
				t.Fatal(err)
			}
			first, err := session.Recv(ctx)
			if err != nil || first != (asr.Event{Type: asr.EventFinal, Text: "第一句"}) {
				t.Fatalf("first event: %+v, %v", first, err)
			}

			concrete := session.(*Stream)
			assertReleased := func() {
				t.Helper()
				concrete.readMu.Lock()
				defer concrete.readMu.Unlock()
				if concrete.confirmedText.Len() != 0 || concrete.finalSeen != nil ||
					concrete.finalMetadata != nil || concrete.pendingDetails != nil || concrete.pendingText != "" ||
					concrete.lastFinalText != "" {
					t.Fatal("closed session retained historical recognition state")
				}
			}
			switch tc.action {
			case "cancel":
				call, cancel := context.WithCancel(context.Background())
				cancel()
				_, firstErr := session.Recv(call)
				if !errors.Is(firstErr, context.Canceled) {
					t.Fatalf("canceled Recv returned %v, want context.Canceled", firstErr)
				}
				_, repeatErr := session.Recv(context.Background())
				if repeatErr != firstErr {
					t.Fatalf("terminal cancellation was lost: first=%v next=%v", firstErr, repeatErr)
				}
			case "close":
				if err := session.Close(); err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 2; i++ {
					_, err := session.Recv(context.Background())
					if !errors.Is(err, asr.ErrStreamClosed) {
						t.Fatalf("Recv %d after explicit Close = %v", i, err)
					}
				}
			case "finished":
				assertReleased()
				// Simulate an in-flight sender failing after the final
				// frame won the terminal-state race.
				late := errors.New("late write failure")
				if err := concrete.failWrite(late); err != late {
					t.Fatalf("sender error = %v", err)
				}
				stop() // Late parent cancellation must not discard the backlog.
				if err := concrete.terminalFailure(); err != nil {
					t.Fatalf("completed stream overwritten: %v", err)
				}
				canceled, cancel := context.WithCancel(context.Background())
				cancel()
				second, err := session.Recv(canceled)
				if err != nil || second != (asr.Event{Type: asr.EventFinal, Text: "第二句"}) {
					t.Fatalf("second event from final frame: %+v, %v", second, err)
				}
				for i := 0; i < 2; i++ {
					if _, err := session.Recv(canceled); !errors.Is(err, io.EOF) {
						t.Fatalf("Recv %d after final frame = %v, want EOF", i, err)
					}
				}
			}
			assertReleased()
			concrete.readMu.Lock()
			defer concrete.readMu.Unlock()
			if concrete.queue != nil {
				t.Fatal("closed session retained drained or abandoned queue")
			}
		})
	}
}
