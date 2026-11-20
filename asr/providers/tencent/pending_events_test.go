package tencent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/gorilla/websocket"
)

// A V2 sentence_list produces multiple queued SDK events per WebSocket frame.
// Only a provider-confirmed final frame may leave them readable after socket
// shutdown; a caller's explicit Close must discard the pending backlog.
func TestTencentV2PendingEventsAfterClose(t *testing.T) {
	for _, tc := range []struct {
		name  string
		final int
	}{
		{name: "explicit close discards pending events", final: 0},
		{name: "provider final drains pending events", final: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := testClient(t, func(ws *websocket.Conn, req *http.Request) {
				id := req.URL.Query().Get("voice_id")
				if err := sendResponse(ws, id, 0, 0, nil); err != nil {
					t.Errorf("handshake: %v", err)
					return
				}
				if _, _, err := ws.ReadMessage(); err != nil {
					return
				}
				payload, err := json.Marshal(map[string]any{
					"code": 0, "voice_id": id, "final": tc.final,
					"sentences": map[string]any{"sentence_list": []map[string]any{
						{"sentence_id": 1, "sentence_type": 1, "sentence": "第一句"},
						{"sentence_id": 2, "sentence_type": 1, "sentence": "第二句"},
					}},
				})
				if err != nil {
					t.Errorf("marshal: %v", err)
					return
				}
				if err := ws.WriteMessage(websocket.TextMessage, payload); err != nil {
					t.Errorf("write sentence batch: %v", err)
					return
				}
				// Keep a non-final connection open until the client closes it.
				if tc.final == 0 {
					_, _, _ = ws.ReadMessage()
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			stream, err := client.OpenStream(ctx, pcmConfig())
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			if err := stream.Send(ctx, []byte{1, 0}); err != nil {
				t.Fatal(err)
			}
			ev, err := stream.Recv(ctx)
			if err != nil || ev != (asr.Event{Type: asr.EventFinal, Text: "第一句"}) {
				t.Fatalf("first queued event: %+v, %v", ev, err)
			}
			if err := stream.Close(); err != nil {
				t.Fatal(err)
			}
			if tc.final == 0 {
				for i := 0; i < 2; i++ {
					_, err := stream.Recv(context.Background())
					if !errors.Is(err, asr.ErrStreamClosed) {
						t.Fatalf("Recv %d after explicit Close = %v, want ErrStreamClosed", i, err)
					}
				}
				return
			}
			// A final provider frame closes the socket internally, but the
			// batch's remaining confirmed event must still be delivered.
			ev, err = stream.Recv(context.Background())
			if err != nil || ev != (asr.Event{Type: asr.EventFinal, Text: "第二句"}) {
				t.Fatalf("remaining final event: %+v, %v", ev, err)
			}
			for i := 0; i < 2; i++ {
				if _, err := stream.Recv(context.Background()); !errors.Is(err, io.EOF) {
					t.Fatalf("Recv %d after final batch = %v, want EOF", i, err)
				}
			}
		})
	}
}
