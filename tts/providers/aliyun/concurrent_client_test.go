package aliyun

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Shared Client instances must isolate the request, task ID, audio and
// connection for each independent batch or streaming synthesis.
func TestCosyVoiceSharedClientConcurrentSessions(t *testing.T) {
	const sessions = 8
	var active atomic.Int32
	closed := make(chan struct{}, sessions)
	serverErrors := make(chan error, sessions)
	endpoint := mockTTS(t, func(ws *websocket.Conn, _ *http.Request) {
		active.Add(1)
		defer func() {
			active.Add(-1)
			closed <- struct{}{}
		}()
		run, err := readTTSCommand(ws, "run-task", "")
		if err != nil {
			serverErrors <- err
			return
		}
		id := run["__task_id"].(string)
		if err := sendTTSControl(ws, "task-started", id, ""); err != nil {
			serverErrors <- err
			return
		}
		cont, err := readTTSCommand(ws, "continue-task", id)
		if err != nil {
			serverErrors <- err
			return
		}
		input, ok := cont["input"].(map[string]any)
		if !ok {
			serverErrors <- fmt.Errorf("missing input for %s", id)
			return
		}
		text, ok := input["text"].(string)
		if !ok || text == "" {
			serverErrors <- fmt.Errorf("invalid text for %s", id)
			return
		}
		if _, err := readTTSCommand(ws, "finish-task", id); err != nil {
			serverErrors <- err
			return
		}
		if err := ws.WriteMessage(websocket.BinaryMessage, []byte("audio:"+text)); err != nil {
			serverErrors <- err
			return
		}
		if err := sendTTSControl(ws, "task-finished", id, ""); err != nil {
			serverErrors <- err
			return
		}
		_, _, err = ws.ReadMessage()
		if err == nil {
			serverErrors <- fmt.Errorf("socket remained open after synthesis %s", id)
		}
	})
	client, err := New(Config{APIKey: "test", Endpoint: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	results := make(chan error, sessions)
	for i := 0; i < sessions; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := testRequest()
			req.Text = fmt.Sprintf("concurrent-%02d", i)
			want := "audio:" + req.Text
			if i%2 == 0 {
				out, err := client.Synthesize(ctx, req)
				if err != nil {
					results <- fmt.Errorf("batch %d: %w", i, err)
				} else if string(out.Data) != want || out.Format != req.Format {
					results <- fmt.Errorf("batch %d: wrong audio or format", i)
				}
				return
			}
			stream, err := client.SynthesizeStream(ctx, req)
			if err != nil {
				results <- fmt.Errorf("stream %d startup: %w", i, err)
				return
			}
			defer stream.Close()
			var received []byte
			for {
				chunk, err := stream.Recv(ctx)
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					results <- fmt.Errorf("stream %d recv: %w", i, err)
					return
				}
				if chunk.Format != req.Format {
					results <- fmt.Errorf("stream %d: incorrect format", i)
					return
				}
				received = append(received, chunk.Data...)
			}
			if string(received) != want {
				results <- fmt.Errorf("stream %d: audio belongs to a different request", i)
			}
		}(i)
	}
	wg.Wait()
	close(results)
	for err := range results {
		t.Error(err)
	}
	for i := 0; i < sessions; i++ {
		select {
		case <-closed:
		case <-ctx.Done():
			t.Fatalf("only %d/%d server sessions closed: %v", i, sessions, ctx.Err())
		}
	}
	close(serverErrors)
	for err := range serverErrors {
		t.Error(err)
	}
	if n := active.Load(); n != 0 {
		t.Errorf("%d server sessions still active", n)
	}
}
