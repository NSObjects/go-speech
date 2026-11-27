package aliyun

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/transport"
	"github.com/gorilla/websocket"
)

func TestPooledASRReusesOnlyCompletedTasks(t *testing.T) {
	var sockets atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		sockets.Add(1)
		seen := make(map[string]bool)
		for {
			var run taskMessage
			if ws.ReadJSON(&run) != nil {
				return
			}
			id := run.Header.TaskID
			if id == "" || seen[id] || run.Header.Action != "run-task" {
				t.Error("invalid or repeated task ID")
				return
			}
			seen[id] = true
			event := func(name string) error {
				return ws.WriteJSON(map[string]any{"header": map[string]any{"event": name, "task_id": id}})
			}
			if event("task-started") != nil {
				return
			}
			typ, _, err := ws.ReadMessage()
			if err != nil {
				return
			}
			if typ != websocket.BinaryMessage {
				t.Error("expected audio")
				return
			}
			var end taskMessage
			if ws.ReadJSON(&end) != nil {
				return
			}
			if end.Header.Action != "finish-task" || end.Header.TaskID != id {
				t.Error("invalid finish-task")
				return
			}
			if event("task-finished") != nil {
				return
			}
		}
	}))
	defer server.Close()
	client, err := New(Config{
		APIKey: "test", Endpoint: "ws" + strings.TrimPrefix(server.URL, "http") + "/api-ws/v1/inference",
		Model: ModelFunASRRealtime, Pool: &transport.PoolConfig{MaxOpen: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg := asr.Config{Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1}}
	for range 2 {
		stream, err := client.OpenStream(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := stream.Send(ctx, []byte{0, 0}); err != nil {
			t.Fatal(err)
		}
		if err := stream.CloseSend(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := stream.Recv(ctx); !errors.Is(err, io.EOF) {
			t.Fatalf("EOF: %v", err)
		}
		_ = stream.Close()
	}
	if got := sockets.Load(); got != 1 {
		t.Fatalf("expected one socket, got %d", got)
	}
	aborted, err := client.OpenStream(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	_ = aborted.Close()
	next, err := client.OpenStream(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if got := sockets.Load(); got != 2 {
		t.Fatalf("aborted socket was reused: %d connections", got)
	}
}
