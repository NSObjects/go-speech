package aliyun

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
			Payload struct {
				Parameters map[string]any `json:"parameters"`
			} `json:"payload"`
		}
		if err := conn.ReadJSON(&run); err != nil {
			t.Error(err)
			return
		}
		if run.Payload.Parameters["word_timestamp_enabled"] != true {
			t.Error("timestamp flag missing")
		}
		send := func(event, output string) {
			raw := fmt.Sprintf(`{"header":{"event":%q,"task_id":%q},"payload":{"output":%s}}`, event, run.Header.TaskID, output)
			if err := conn.WriteMessage(websocket.TextMessage, []byte(raw)); err != nil {
				t.Error(err)
			}
		}
		send("task-started", "{}")
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Error(err)
			return
		}
		send("result-generated", `{"type":"sentence-begin","sentence":{"index":0,"words":[{"text":"你","begin_time":0,"end_time":100}]}}`)
		send("result-generated", `{"type":"sentence-synthesis","sentence":{"index":0,"words":[{"text":"你","begin_time":0,"end_time":100}]}}`)
		if err := conn.WriteMessage(websocket.BinaryMessage, []byte("audio")); err != nil {
			t.Error(err)
			return
		}
		send("result-generated", `{"type":"sentence-end","original_text":"你","normalized_text":"你","sentence":{"index":0,"words":[{"text":"你","begin_index":0,"end_index":1,"begin_time":0,"end_time":200}]}}`)
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Error(err)
			return
		}
		send("task-finished", "{}")
	}))
	defer srv.Close()
	client, err := New(Config{APIKey: "test", Endpoint: "ws" + strings.TrimPrefix(srv.URL, "http") + "/api-ws/v1/inference"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req := tts.Request{Text: "你", Voice: "longanyang", Options: tts.Options{Provider: Options{WordTimestampEnabled: &on}}}
	if _, err := client.Synthesize(ctx, req); err == nil {
		t.Fatal("audio-only synthesis accepted timestamps")
	}
	s, err := client.SynthesizeStream(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	reader := s.(tts.EventReceiver)
	e, err := reader.RecvEvent(ctx)
	if err != nil || e.Type != tts.EventAudio || e.Audio == nil || string(e.Audio.Data) != "audio" {
		t.Fatalf("audio = %+v, %v", e, err)
	}
	if _, err := s.Recv(ctx); !errors.Is(err, tts.ErrReceiveMode) {
		t.Fatalf("mixed receive = %v", err)
	}
	e, err = reader.RecvEvent(ctx)
	if err != nil || e.Type != tts.EventAlignment || e.Alignment == nil {
		t.Fatalf("alignment = %+v, %v", e, err)
	}
	a := e.Alignment
	if a.Text != "你" || len(a.Words) != 1 || a.Words[0].StartTime != 0 || a.Words[0].EndTime != 200*time.Millisecond {
		t.Fatalf("final alignment = %+v", a)
	}
	d, ok := a.Provider.(AlignmentDetails)
	if !ok || d.SentenceIndex == nil || *d.SentenceIndex != 0 || len(d.Indices) != 1 || d.Indices[0].Begin == nil || *d.Indices[0].Begin != 0 {
		t.Fatalf("details = %+v", a.Provider)
	}
	for range 2 {
		if _, err := reader.RecvEvent(ctx); !errors.Is(err, io.EOF) {
			t.Fatalf("expected stable EOF, got %v", err)
		}
	}
}

func TestInvalidAlignmentTimes(t *testing.T) {
	for _, raw := range []string{
		`{"payload":{"output":{"type":"sentence-end","sentence":{"words":[{"text":"a","end_time":1}]}}}}`,
		`{"payload":{"output":{"type":"sentence-end","sentence":{"words":[{"text":"a","begin_time":2,"end_time":1}]}}}}`,
		`{"payload":{"output":{"type":"sentence-end","sentence":{"words":[{"text":"a","begin_time":0,"end_time":9223372036854775807}]}}}}`,
	} {
		if _, err := decodeAlignment([]byte(raw)); err == nil {
			t.Error("invalid timestamp accepted")
		}
	}
}
