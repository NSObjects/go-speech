package aliyun

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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

func lifecycleEndpoint(t *testing.T, afterStart func(*websocket.Conn, string)) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer c.Close()
		_, msg, err := c.ReadMessage()
		if err != nil {
			t.Errorf("read run-task: %v", err)
			return
		}
		var task taskMessage
		if err := json.Unmarshal(msg, &task); err != nil {
			t.Errorf("decode run-task: %v", err)
			return
		}
		if task.Header.Action != "run-task" || task.Header.TaskID == "" {
			t.Errorf("invalid run-task: %+v", task.Header)
			return
		}
		if err := c.WriteJSON(map[string]any{
			"header": map[string]any{"event": "task-started", "task_id": task.Header.TaskID},
			"payload": map[string]any{"output": map[string]any{}},
		}); err != nil {
			t.Errorf("send task-started: %v", err)
			return
		}
		afterStart(c, task.Header.TaskID)
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http") + "/api-ws/v1/inference"
}

func openLifecycleSession(t *testing.T, ctx context.Context, endpoint string) asr.Stream {
	t.Helper()
	client, err := New(Config{Endpoint: endpoint, APIKey: "test", Model: ModelFunASRRealtime})
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.OpenStream(ctx, asr.Config{
		Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestRecvTerminalFailurePreservesReason(t *testing.T) {
	cases := []struct {
		name       string
		kind       string
		want       speecherr.Code
		remoteCode string
		retryable  bool
	}{
		{name: "remote auth", kind: "auth", want: speecherr.CodeUnauthorized, remoteCode: "AuthenticationFailed"},
		{name: "remote limit", kind: "limit", want: speecherr.CodeRateLimited, remoteCode: "Throttling", retryable: true},
		{name: "malformed JSON", kind: "malformed", want: speecherr.CodeProtocol},
		{name: "task ID mismatch", kind: "mismatch", want: speecherr.CodeProtocol},
		{name: "abrupt disconnect", kind: "disconnect", want: speecherr.CodeUnavailable, retryable: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := lifecycleEndpoint(t, func(ws *websocket.Conn, id string) {
				switch tc.kind {
				case "auth", "limit":
					_ = ws.WriteJSON(map[string]any{
						"header": map[string]any{
							"event": "task-failed", "task_id": id,
							"error_code": tc.remoteCode, "error_message": "test failure",
						},
					})
				case "malformed":
					_ = ws.WriteMessage(websocket.TextMessage, []byte("{broken"))
				case "mismatch":
					_ = ws.WriteJSON(map[string]any{
						"header": map[string]any{"event": "result-generated", "task_id": "wrong-id"},
					})
				case "disconnect":
					_ = ws.WriteControl(websocket.CloseMessage,
						websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "unavailable"),
						time.Now().Add(time.Second))
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			s := openLifecycleSession(t, ctx, endpoint)
			_, first := s.Recv(ctx)
			if !speecherr.IsCode(first, tc.want) {
				t.Fatalf("first error = %v, want %s", first, tc.want)
			}
			var structured *speecherr.Error
			if !errors.As(first, &structured) {
				t.Fatalf("missing shared speech error: %v", first)
			}
			if structured.RemoteCode != tc.remoteCode || structured.Retryable != tc.retryable {
				t.Fatalf("classification: %+v", structured)
			}
			// Previous behavior replaced this useful failure with ErrClosed.
			_, second := s.Recv(context.Background())
			if second != first {
				t.Fatalf("terminal error was lost: first %v, second %v", first, second)
			}
			if err := s.Send(context.Background(), []byte{0, 0}); !errors.Is(err, asr.ErrStreamClosed) {
				t.Fatalf("Send after failure must be closed, got %v", err)
			}
		})
	}
}

func TestCloseAndCancelledRecvAreNotNetworkFailures(t *testing.T) {
	endpoint := lifecycleEndpoint(t, func(ws *websocket.Conn, _ string) {
		// Keep the server open until the caller closes/cancels the client.
		_, _, _ = ws.ReadMessage()
	})
	t.Run("explicit close", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		s := openLifecycleSession(t, ctx, endpoint)
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if err := s.Send(ctx, []byte{0, 0}); !errors.Is(err, asr.ErrStreamClosed) {
			t.Fatalf("send on closed stream: %v", err)
		}
		if err := s.CloseSend(ctx); !errors.Is(err, asr.ErrStreamClosed) {
			t.Fatalf("finish closed stream: %v", err)
		}
		_, err := s.Recv(ctx)
		if !errors.Is(err, asr.ErrStreamClosed) || speecherr.IsCode(err, speecherr.CodeUnavailable) {
			t.Fatalf("explicit close incorrectly classified: %v", err)
		}
	})
	t.Run("cancel parent during blocked Recv", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		s := openLifecycleSession(t, ctx, endpoint)
		result := make(chan error, 1)
		go func() {
			_, err := s.Recv(context.Background())
			result <- err
		}()
		cancel()
		select {
		case err := <-result:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel converted into network failure: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("parent cancellation failed to unblock Recv")
		}
	})
	t.Run("cancel per-call Recv", func(t *testing.T) {
		parent, stop := context.WithTimeout(context.Background(), 4*time.Second)
		defer stop()
		s := openLifecycleSession(t, parent, endpoint)
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() {
			_, err := s.Recv(ctx)
			result <- err
		}()
		cancel()
		select {
		case err := <-result:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel converted into network failure: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("operation cancellation did not unblock Recv")
		}
	})
}

func TestRecvEOFAfterTaskFinishedIsStable(t *testing.T) {
	endpoint := lifecycleEndpoint(t, func(ws *websocket.Conn, id string) {
		_ = ws.WriteJSON(map[string]any{
			"header": map[string]any{"event": "task-finished", "task_id": id},
		})
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s := openLifecycleSession(t, ctx, endpoint)
	for i := 0; i < 3; i++ {
		_, err := s.Recv(ctx)
		if !errors.Is(err, io.EOF) {
			t.Fatalf("Recv call %d should return EOF, got %v", i, err)
		}
	}
}
