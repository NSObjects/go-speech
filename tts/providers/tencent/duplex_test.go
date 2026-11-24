package tencent

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

	"github.com/NSObjects/go-speech/tts"
	"github.com/gorilla/websocket"
)

func TestDuplexIncrementalText(t *testing.T) {
	upgrader := websocket.Upgrader{}
	received := make(chan []string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session := r.URL.Query().Get("SessionId")
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteJSON(map[string]any{"code": 0, "session_id": session, "ready": 1})
		var parts []string
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg struct {
				Action string `json:"action"`
				Data   string `json:"data"`
			}
			if json.Unmarshal(raw, &msg) != nil {
				return
			}
			if msg.Action == "ACTION_SYNTHESIS" {
				parts = append(parts, msg.Data)
				if len(parts) == 1 {
					_ = conn.WriteMessage(websocket.BinaryMessage, []byte("audio"))
				}
			}
			if msg.Action == "ACTION_COMPLETE" {
				received <- parts
				_ = conn.WriteJSON(map[string]any{"code": 0, "session_id": session, "final": 1})
				return
			}
		}
	}))
	defer srv.Close()
	client, err := New(Config{AppID: "123", SecretID: "secret", SecretKey: "key", Endpoint: "ws" + strings.TrimPrefix(srv.URL, "http") + "/stream_wsv2"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := client.OpenDuplex(ctx, tts.DuplexRequest{Voice: "101001"})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err := stream.SendText(ctx, "你好"); err != nil {
		t.Fatal(err)
	}
	chunk, err := stream.Recv(ctx)
	if err != nil || string(chunk.Data) != "audio" {
		t.Fatalf("audio before half-close: %q %v", chunk.Data, err)
	}
	if err := stream.SendText(ctx, "世界"); err != nil {
		t.Fatal(err)
	}
	if err := stream.CloseSend(ctx); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(stream.SendText(ctx, "again"), tts.ErrInputClosed) {
		t.Fatal("expected ErrInputClosed")
	}
	if _, err := stream.Recv(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
	got := <-received
	if len(got) != 2 || got[0] != "你好" || got[1] != "世界" {
		t.Fatalf("fragments: %q", got)
	}
}
