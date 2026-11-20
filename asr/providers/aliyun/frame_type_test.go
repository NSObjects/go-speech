package aliyun

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/gorilla/websocket"
)

// Aliyun's run-task and recognition notifications are JSON text frames.
// A binary WebSocket frame that happens to contain valid JSON is still a
// protocol violation; accepting it would conceal vendor wire incompatibility.
func TestAliyunRejectsBinaryJSONControlFrames(t *testing.T) {
	t.Run("task_started_handshake", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
			if err != nil {
				t.Errorf("upgrade: %v", err)
				return
			}
			defer ws.Close()
			_, data, err := ws.ReadMessage()
			if err != nil {
				t.Errorf("read run-task: %v", err)
				return
			}
			var task taskMessage
			if err := json.Unmarshal(data, &task); err != nil {
				t.Errorf("decode run-task: %v", err)
				return
			}
			response, err := json.Marshal(map[string]any{
				"header": map[string]any{"event": "task-started", "task_id": task.Header.TaskID},
			})
			if err != nil {
				t.Errorf("encode task-started: %v", err)
				return
			}
			if err := ws.WriteMessage(websocket.BinaryMessage, response); err != nil {
				t.Errorf("send malformed task-started: %v", err)
			}
		}))
		defer server.Close()
		endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + "/api-ws/v1/inference"
		client, err := New(Config{Endpoint: endpoint, APIKey: "test", Model: ModelFunASRRealtime})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		stream, err := client.OpenStream(ctx, asr.Config{
			Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
		})
		if stream != nil {
			defer stream.Close()
			t.Fatalf("binary task-started frame returned a stream")
		}
		if !speecherr.IsCode(err, speecherr.CodeProtocol) {
			t.Fatalf("binary task-started frame = %v, want CodeProtocol", err)
		}
	})

	t.Run("recognition_result", func(t *testing.T) {
		endpoint := lifecycleEndpoint(t, func(ws *websocket.Conn, id string) {
			payload, err := json.Marshal(map[string]any{
				"header": map[string]any{"event": "result-generated", "task_id": id},
				"payload": map[string]any{"output": map[string]any{
					"sentence": map[string]any{"text": "synthetic notification", "sentence_end": true},
				}},
			})
			if err != nil {
				t.Errorf("encode result: %v", err)
				return
			}
			if err := ws.WriteMessage(websocket.BinaryMessage, payload); err != nil {
				t.Errorf("write binary result: %v", err)
			}
		})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s := openLifecycleSession(t, ctx, endpoint)
		_, first := s.Recv(ctx)
		if !speecherr.IsCode(first, speecherr.CodeProtocol) {
			t.Fatalf("binary recognition event = %v, want CodeProtocol", first)
		}
		for i := 0; i < 2; i++ {
			_, next := s.Recv(context.Background())
			if !errors.Is(next, first) || next != first {
				t.Fatalf("Recv %d lost terminal protocol error: %v vs %v", i, next, first)
			}
		}
		if err := s.Send(context.Background(), []byte{0, 0}); !errors.Is(err, asr.ErrStreamClosed) {
			t.Fatalf("Send after protocol failure = %v", err)
		}
	})
}
