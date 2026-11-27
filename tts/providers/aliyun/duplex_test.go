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
	"unicode/utf8"

	"github.com/NSObjects/go-speech/speecherr"
	"github.com/NSObjects/go-speech/tts"
	"github.com/gorilla/websocket"
)

func TestDuplexIncrementalText(t *testing.T) {
	upgrader := websocket.Upgrader{}
	received := make(chan []string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var run struct {
			Header struct {
				TaskID string `json:"task_id"`
			} `json:"header"`
		}
		if json.Unmarshal(raw, &run) != nil {
			return
		}
		_ = conn.WriteJSON(map[string]any{"header": map[string]any{"event": "task-started", "task_id": run.Header.TaskID}})
		var fragments []string
		for {
			_, raw, err = conn.ReadMessage()
			if err != nil {
				return
			}
			var msg struct {
				Header struct {
					Action string `json:"action"`
				} `json:"header"`
				Payload struct {
					Input struct {
						Text string `json:"text"`
					} `json:"input"`
				} `json:"payload"`
			}
			if json.Unmarshal(raw, &msg) != nil {
				return
			}
			if msg.Header.Action == "continue-task" {
				fragments = append(fragments, msg.Payload.Input.Text)
				if len(fragments) == 1 {
					_ = conn.WriteMessage(websocket.BinaryMessage, []byte("audio"))
				}
			}
			if msg.Header.Action == "finish-task" {
				received <- fragments
				_ = conn.WriteJSON(map[string]any{"header": map[string]any{"event": "task-finished", "task_id": run.Header.TaskID}})
				return
			}
		}
	}))
	defer srv.Close()
	client, err := New(Config{APIKey: "test", Endpoint: "ws" + strings.TrimPrefix(srv.URL, "http") + "/api-ws/v1/inference"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := client.OpenDuplex(ctx, tts.DuplexRequest{Voice: "longanyang"})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err := stream.SendText(ctx, "你好"); err != nil {
		t.Fatal(err)
	}
	chunk, err := stream.Recv(ctx)
	if err != nil || string(chunk.Data) != "audio" {
		t.Fatalf("expected audio before CloseSend: %q %v", chunk.Data, err)
	}
	if err := stream.SendText(ctx, "世界"); err != nil {
		t.Fatal(err)
	}
	// Two 2-rune fragments were already sent. Fill the remaining session
	// budget in valid increments; an overflowing fragment must not be sent
	// or consume budget, and the caller may continue with a smaller one.
	left := maxSessionTextRunes - 4
	for left > maxTextRunes {
		if err := stream.SendText(ctx, strings.Repeat("界", maxTextRunes)); err != nil {
			t.Fatal(err)
		}
		left -= maxTextRunes
	}
	if err := stream.SendText(ctx, strings.Repeat("界", left-1)); err != nil {
		t.Fatal(err)
	}
	if err := stream.SendText(ctx, "多余"); !speecherr.IsCode(err, speecherr.CodeInvalidArgument) {
		t.Fatalf("overflow accepted: %v", err)
	}
	if err := stream.SendText(ctx, "终"); err != nil {
		t.Fatalf("rejected fragment consumed budget: %v", err)
	}
	if err := stream.SendText(ctx, "多"); !speecherr.IsCode(err, speecherr.CodeInvalidArgument) {
		t.Fatalf("session budget exceeded: %v", err)
	}
	if err := stream.CloseSend(ctx); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(stream.SendText(ctx, "again"), tts.ErrInputClosed) {
		t.Fatal("SendText after CloseSend must fail")
	}
	if _, err := stream.Recv(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
	got := <-received
	if len(got) < 4 || got[0] != "你好" || got[1] != "世界" || got[len(got)-1] != "终" {
		t.Fatal("unexpected transmitted fragment sequence")
	}
	count := 0
	for _, part := range got {
		count += utf8.RuneCountInString(part)
		if strings.Contains(part, "多") {
			t.Fatal("rejected fragment reached the server")
		}
	}
	if count != maxSessionTextRunes {
		t.Fatalf("transmitted characters = %d, want %d", count, maxSessionTextRunes)
	}
}
