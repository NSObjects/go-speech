package tencent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/tts"
	"github.com/gorilla/websocket"
)

func TestAlignmentEvents(t *testing.T) {
	on := true
	u := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session := r.URL.Query().Get("SessionId")
		if r.URL.Query().Get("EnableSubtitle") != "true" {
			t.Error("subtitle flag missing")
		}
		conn, err := u.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		send := func(body string) {
			raw := fmt.Sprintf(`{"code":0,"session_id":%q,%s}`, session, body)
			if err := conn.WriteMessage(websocket.TextMessage, []byte(raw)); err != nil {
				t.Error(err)
			}
		}
		send(`"ready":1`)
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Error(err)
			return
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, []byte("audio")); err != nil {
			t.Error(err)
			return
		}
		send(`"final":0,"result":{"subtitles":[{"Text":"你","BeginTime":0,"EndTime":200,"BeginIndex":0,"EndIndex":1,"Phoneme":"ni3"}]}`)
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Error(err)
			return
		}
		send(`"final":1,"result":{"subtitles":[{"Text":"好","BeginTime":200,"EndTime":400,"BeginIndex":1,"EndIndex":2,"Phoneme":null}]}`)
	}))
	defer srv.Close()
	client, err := New(Config{AppID: "123", SecretID: "id", SecretKey: "key", Endpoint: "ws" + strings.TrimPrefix(srv.URL, "http") + "/stream_wsv2"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	opts := tts.Options{Provider: Options{EnableSubtitle: &on}}
	if _, err := client.Synthesize(ctx, tts.Request{Text: "你好", Voice: "101001", Options: opts}); err == nil {
		t.Fatal("audio-only synthesis accepted timestamps")
	}
	s, err := client.OpenDuplex(ctx, tts.DuplexRequest{Voice: "101001", Options: opts})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SendText(ctx, "你好"); err != nil {
		t.Fatal(err)
	}
	reader := s.(tts.EventReceiver)
	e, err := reader.RecvEvent(ctx)
	if err != nil || e.Type != tts.EventAudio || e.Audio == nil || string(e.Audio.Data) != "audio" {
		t.Fatalf("audio = %+v, %v", e, err)
	}
	if _, err := s.Recv(ctx); !errors.Is(err, tts.ErrReceiveMode) {
		t.Fatalf("mixed receive = %v", err)
	}
	e, err = reader.RecvEvent(ctx)
	if err != nil || e.Type != tts.EventAlignment || e.Alignment == nil || len(e.Alignment.Words) != 1 {
		t.Fatalf("alignment = %+v, %v", e, err)
	}
	if w := e.Alignment.Words[0]; w.Phoneme != "ni3" || w.EndTime != 200*time.Millisecond {
		t.Fatalf("word = %+v", w)
	}
	d, ok := e.Alignment.Provider.(AlignmentDetails)
	if !ok || len(d.Indices) != 1 || d.Indices[0].Begin == nil || *d.Indices[0].Begin != 0 {
		t.Fatalf("details = %+v", e.Alignment.Provider)
	}
	if err := s.CloseSend(ctx); err != nil {
		t.Fatal(err)
	}
	e, err = reader.RecvEvent(ctx)
	if err != nil || e.Type != tts.EventAlignment || e.Alignment == nil || len(e.Alignment.Words) != 1 {
		t.Fatalf("final alignment = %+v, %v", e, err)
	}
	if w := e.Alignment.Words[0]; w.Phoneme != "" || w.StartTime != 200*time.Millisecond || w.EndTime != 400*time.Millisecond {
		t.Fatalf("last word = %+v", w)
	}
	for range 2 {
		if _, err := reader.RecvEvent(ctx); !errors.Is(err, io.EOF) {
			t.Fatalf("expected stable EOF, got %v", err)
		}
	}
}

func TestInvalidAlignmentTimes(t *testing.T) {
	for _, raw := range []string{
		`{"result":{"subtitles":[{"Text":"a","EndTime":1}]}}`,
		`{"result":{"subtitles":[{"Text":"a","BeginTime":2,"EndTime":1}]}}`,
		`{"result":{"subtitles":[{"Text":"a","BeginTime":0,"EndTime":9223372036854775807}]}}`,
	} {
		if _, err := decodeAlignment([]byte(raw)); err == nil {
			t.Error("invalid timestamp accepted")
		}
	}
}

func TestAlignmentPreservesSilencePhoneme(t *testing.T) {
	// Observed in a real WSv2 response: a textless SIL entry with
	// positive duration. Do not invent text or drop the native entry.
	raw := []byte(`{"result":{"subtitles":[{"Text":"好","BeginTime":1600,"EndTime":1820,"BeginIndex":0,"EndIndex":1,"Phoneme":"hao3"},{"Text":"","BeginTime":1820,"EndTime":2040,"Phoneme":"SIL"}]}}`)
	a, err := decodeAlignment(raw)
	if err != nil {
		t.Fatal(err)
	}
	if a == nil || len(a.Words) != 2 {
		t.Fatalf("alignment=%+v", a)
	}
	w := a.Words[1]
	if w.Text != "" || w.Phoneme != "SIL" || w.StartTime != 1820*time.Millisecond || w.EndTime != 2040*time.Millisecond {
		t.Fatalf("silence was changed: %+v", w)
	}
	d, ok := a.Provider.(AlignmentDetails)
	if !ok || len(d.Indices) != len(a.Words) || d.Indices[1].Begin != nil || d.Indices[1].End != nil {
		t.Fatalf("silence indices were fabricated or dropped: %+v", a.Provider)
	}
}
