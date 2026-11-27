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
	"unicode/utf8"

	"github.com/NSObjects/go-speech/speecherr"
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
	// Two 2-rune fragments were already sent. Fill the remaining session
	// budget in valid increments; an overflowing fragment must not be sent
	// or consume budget, and the caller may continue with a smaller one.
	left := maxTextRunes - 4
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
	if err := stream.(tts.Resetter).Reset(ctx); err != nil {
		t.Fatal(err)
	}
	if err := stream.SendText(ctx, "多"); !speecherr.IsCode(err, speecherr.CodeInvalidArgument) {
		t.Fatalf("session budget exceeded: %v", err)
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
	if count != maxTextRunes {
		t.Fatalf("transmitted characters = %d, want %d", count, maxTextRunes)
	}
}
