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

// A pending V2 sentence must not hide a canceled Recv operation while the
// session remains active. A provider-confirmed terminal batch is different:
// its queued final sentences must remain drainable before stable EOF.
func TestTencentCanceledRecvWithPendingV2SentenceBatch(t *testing.T) {
	for _, tc := range []struct {
		name  string
		final int
	}{
		{name: "active stream cancellation terminates before queued event", final: 0},
		{name: "completed stream drains queued events despite later cancellation", final: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := testClient(t, func(ws *websocket.Conn, req *http.Request) {
				id := req.URL.Query().Get("voice_id")
				if err := sendResponse(ws, id, 0, 0, nil); err != nil {
					return
				}
				if _, _, err := ws.ReadMessage(); err != nil {
					return
				}
				payload, err := json.Marshal(map[string]any{
					"code": 0, "voice_id": id, "final": tc.final,
					"sentences": map[string]any{"sentence_list": []map[string]any{
						{"sentence_id": 0, "sentence_type": 1, "sentence": "第一句"},
						{"sentence_id": 1, "sentence_type": 1, "sentence": "第二句"},
					}},
				})
				if err != nil {
					t.Errorf("marshal response: %v", err)
					return
				}
				if err := ws.WriteMessage(websocket.TextMessage, payload); err != nil {
					return
				}
				if tc.final == 0 {
					_, _, _ = ws.ReadMessage() // Wait for caller cancellation/close.
				}
			})
			parent, stop := context.WithTimeout(context.Background(), 3*time.Second)
			defer stop()
			s, err := client.OpenStream(parent, pcmConfig())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err := s.Send(parent, []byte{1, 0}); err != nil {
				t.Fatal(err)
			}
			got, err := s.Recv(parent)
			want := asr.Event{Type: asr.EventFinal, Text: "第一句"}
			if err != nil || got != want {
				t.Fatalf("first V2 result = %+v, %v; want %+v", got, err, want)
			}
			call, cancel := context.WithCancel(context.Background())
			cancel()
			if tc.final == 0 {
				_, first := s.Recv(call)
				if !errors.Is(first, context.Canceled) {
					t.Fatalf("canceled Recv returned %v instead of context.Canceled", first)
				}
				_, second := s.Recv(context.Background())
				if second != first {
					t.Fatalf("terminal cancellation changed: %v vs %v", first, second)
				}
				return
			}
			got, err = s.Recv(call)
			want = asr.Event{Type: asr.EventFinal, Text: "第二句"}
			if err != nil || got != want {
				t.Fatalf("remaining completed final = %+v, %v; want %+v", got, err, want)
			}
			for i := 0; i < 2; i++ {
				if _, err := s.Recv(call); !errors.Is(err, io.EOF) {
					t.Fatalf("completed Recv %d returned %v instead of EOF", i, err)
				}
			}
		})
	}
}
