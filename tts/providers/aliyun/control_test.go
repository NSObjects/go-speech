package aliyun

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/tts"
	"github.com/gorilla/websocket"
)

func TestNativeCancel(t *testing.T) {
	for _, events := range []bool{false, true} {
		t.Run(map[bool]string{false: "audio", true: "events"}[events], func(t *testing.T) {
			u := websocket.Upgrader{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := u.Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
				var run struct {
					Header struct {
						TaskID string `json:"task_id"`
					} `json:"header"`
				}
				if err := conn.ReadJSON(&run); err != nil {
					t.Error(err)
					return
				}
				if err := conn.WriteJSON(map[string]any{"header": map[string]any{"event": "task-started", "task_id": run.Header.TaskID}}); err != nil {
					t.Error(err)
					return
				}
				var cancel struct {
					Header struct {
						Action string `json:"action"`
						TaskID string `json:"task_id"`
					} `json:"header"`
					Payload struct {
						Input map[string]string `json:"input"`
					} `json:"payload"`
				}
				if err := conn.ReadJSON(&cancel); err != nil {
					t.Error(err)
					return
				}
				if cancel.Header.Action != "finish-task" || cancel.Header.TaskID != run.Header.TaskID || cancel.Payload.Input["directive"] != "cancel" {
					t.Errorf("cancel command = %+v", cancel)
					return
				}
				if err := conn.WriteJSON(map[string]any{"header": map[string]any{"event": "task-finished", "task_id": run.Header.TaskID}}); err != nil {
					t.Error(err)
				}
			}))
			defer srv.Close()
			c, err := New(Config{APIKey: "test", Endpoint: "ws" + strings.TrimPrefix(srv.URL, "http") + "/api-ws/v1/inference"})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			s, err := c.OpenDuplex(ctx, tts.DuplexRequest{Voice: "longanyang"})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			receive := func() error {
				if events {
					_, err := s.(tts.EventReceiver).RecvEvent(ctx)
					return err
				}
				_, err := s.Recv(ctx)
				return err
			}
			received := make(chan error, 1)
			go func() { received <- receive() }()
			if err := s.(tts.Canceler).Cancel(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-received:
				if !errors.Is(err, tts.ErrSynthesisCanceled) {
					t.Fatalf("cancel result = %v", err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if err := receive(); !errors.Is(err, tts.ErrSynthesisCanceled) {
				t.Fatalf("terminal result = %v", err)
			}
			if err := s.SendText(ctx, "again"); err == nil {
				t.Fatal("text accepted after cancel")
			}
		})
	}
}
