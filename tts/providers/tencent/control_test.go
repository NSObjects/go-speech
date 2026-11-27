package tencent

import (
	"context"
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

func TestNativeReset(t *testing.T) {
	u := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session := r.URL.Query().Get("SessionId")
		conn, err := u.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		if err := conn.WriteJSON(map[string]any{"code": 0, "session_id": session, "ready": 1}); err != nil {
			t.Error(err)
			return
		}
		for _, want := range []struct{ action, data string }{
			{"ACTION_SYNTHESIS", "first"},
			{"ACTION_RESET", ""},
			{"ACTION_SYNTHESIS", "next"},
			{"ACTION_COMPLETE", ""},
		} {
			var msg struct {
				SessionID string `json:"session_id"`
				MessageID string `json:"message_id"`
				Action    string `json:"action"`
				Data      string `json:"data"`
			}
			if err := conn.ReadJSON(&msg); err != nil {
				t.Error(err)
				return
			}
			if msg.SessionID != session || msg.MessageID == "" || msg.Action != want.action || msg.Data != want.data {
				t.Errorf("message = %+v, want %+v", msg, want)
				return
			}
			if want.action == "ACTION_SYNTHESIS" {
				if err := conn.WriteMessage(websocket.BinaryMessage, []byte(want.data)); err != nil {
					t.Error(err)
					return
				}
			}
		}
		if err := conn.WriteJSON(map[string]any{"code": 0, "session_id": session, "final": 1}); err != nil {
			t.Error(err)
		}
	}))
	defer srv.Close()
	c, err := New(Config{AppID: "123", SecretID: "id", SecretKey: "key", Endpoint: "ws" + strings.TrimPrefix(srv.URL, "http") + "/stream_wsv2"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := c.OpenDuplex(ctx, tts.DuplexRequest{Voice: "101001"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	reset := s.(tts.Resetter)
	if err := s.SendText(ctx, "first"); err != nil {
		t.Fatal(err)
	}
	if err := reset.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.SendText(ctx, "next"); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseSend(ctx); err != nil {
		t.Fatal(err)
	}
	if err := reset.Reset(ctx); !errors.Is(err, tts.ErrInputClosed) {
		t.Fatalf("reset after CloseSend = %v", err)
	}
	for _, want := range []string{"first", "next"} {
		chunk, err := s.Recv(ctx)
		if err != nil || string(chunk.Data) != want {
			t.Fatalf("chunk = %q, %v; want %q", chunk.Data, err, want)
		}
	}
	if _, err := s.Recv(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("end = %v", err)
	}
}
