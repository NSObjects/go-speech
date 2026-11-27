package aliyun

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/transport"
	"github.com/gorilla/websocket"
)

func TestPooledTTSReusesCompletedTasks(t *testing.T) {
	var sockets atomic.Int32
	ids := make(chan string, 8)
	endpoint := mockTTS(t, func(ws *websocket.Conn, _ *http.Request) {
		sockets.Add(1)
		for {
			run, err := readTTSCommand(ws, "run-task", "")
			if err != nil {
				return
			}
			id := run["__task_id"].(string)
			ids <- id
			if sendTTSControl(ws, "task-started", id, "") != nil {
				return
			}
			if _, err := readTTSCommand(ws, "continue-task", id); err != nil {
				return
			}
			end, err := readTTSCommand(ws, "finish-task", id)
			if err != nil {
				return
			}
			input, _ := end["input"].(map[string]any)
			if input["directive"] != "cancel" {
				if ws.WriteMessage(websocket.BinaryMessage, []byte{1, 2}) != nil {
					return
				}
			}
			if sendTTSControl(ws, "task-finished", id, "") != nil {
				return
			}
		}
	})
	client, err := New(Config{APIKey: "test", Endpoint: endpoint, Pool: &transport.PoolConfig{MaxOpen: 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	firstCtx, firstCancel := context.WithCancel(ctx)
	first, err := client.SynthesizeStream(firstCtx, testRequest())
	if err != nil {
		firstCancel()
		t.Fatal(err)
	}
	if _, err := first.Recv(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Recv(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("first EOF: %v", err)
	}
	// The new stream owns the same physical socket. Old Close and context
	// cancellation must not interfere with the new task.
	second, err := client.SynthesizeStream(ctx, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	firstCancel()
	_ = first.Close()
	if _, err := second.Recv(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Recv(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("second EOF: %v", err)
	}
	_ = second.Close()
	third, err := client.Synthesize(ctx, testRequest())
	if err != nil || len(third.Data) != 2 {
		t.Fatalf("third: %v, %v", third.Data, err)
	}
	seen := make(map[string]bool)
	for range 3 {
		select {
		case id := <-ids:
			if seen[id] {
				t.Fatalf("task ID reused: %s", id)
			}
			seen[id] = true
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if got := sockets.Load(); got != 1 {
		t.Fatalf("expected one physical connection, got %d", got)
	}
}
