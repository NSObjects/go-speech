package volcengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/tts"
	"github.com/gorilla/websocket"
)

const alignmentJSON = `{"event":"alignment","text":"你好","words":[{"word":"你","startTime":0,"endTime":0.25},{"word":"好","startTime":0.25,"endTime":0.5}],"phonemes":[{"phoneme":"ni"}]}`

func TestHTTPAlignmentEvents(t *testing.T) {
	on := true
	client := fixtureClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if string(req["enable_subtitle"]) != "true" {
			t.Error("missing subtitle flag")
		}
		for _, line := range []string{readyJSON, audioJSON, alignmentJSON, finalJSON} {
			_, _ = fmt.Fprintln(w, line)
		}
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req := tts.Request{Text: "你好", Voice: fixtureVoice, Options: tts.Options{Provider: Options{EnableSubtitle: &on}}}
	if _, err := client.Synthesize(ctx, req); err == nil {
		t.Fatal("audio-only batch silently accepted subtitle request")
	}
	s, err := client.SynthesizeStream(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	reader := s.(tts.EventReceiver)
	e, err := reader.RecvEvent(ctx)
	if err != nil || e.Type != tts.EventAudio || e.Audio == nil || len(e.Audio.Data) != 3 {
		t.Fatalf("audio event = %+v, %v", e, err)
	}
	if _, err := s.Recv(ctx); !errors.Is(err, tts.ErrReceiveMode) {
		t.Fatalf("mixed receiver = %v", err)
	}
	e, err = reader.RecvEvent(ctx)
	checkAlignment(t, e, err)
	for range 2 {
		if _, err := reader.RecvEvent(ctx); !errors.Is(err, io.EOF) {
			t.Fatalf("expected stable EOF, got %v", err)
		}
	}
}

func checkAlignment(t *testing.T, e tts.Event, err error) {
	t.Helper()
	if err != nil || e.Type != tts.EventAlignment || e.Alignment == nil {
		t.Fatalf("alignment event = %+v, %v", e, err)
	}
	a := e.Alignment
	if a.Text != "你好" || len(a.Words) != 2 || a.Words[0].Text != "你" ||
		a.Words[0].StartTime != 0 || a.Words[0].EndTime != 250*time.Millisecond ||
		a.Words[1].EndTime != 500*time.Millisecond {
		t.Fatalf("alignment = %+v", a)
	}
	p, ok := a.Provider.(AlignmentDetails)
	if !ok || len(p.Phonemes) != 1 || string(p.Phonemes[0]) != `{"phoneme":"ni"}` {
		t.Fatalf("native phonemes = %+v", a.Provider)
	}
}

func TestDuplexLateAlignment(t *testing.T) {
	upgrader := websocket.Upgrader{}
	on := true
	client := fixtureClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		var init clientEvent
		if err := conn.ReadJSON(&init); err != nil || init.EnableSubtitle == nil || !*init.EnableSubtitle {
			t.Errorf("init = %+v, %v", init, err)
			return
		}
		if err := conn.WriteJSON(event{Event: "ready", SessionID: init.SessionID}); err != nil {
			t.Error(err)
			return
		}
		for _, want := range []string{"text", "flush", "close"} {
			var msg clientEvent
			if err := conn.ReadJSON(&msg); err != nil || msg.Event != want {
				t.Errorf("expected %s, got %+v, %v", want, msg, err)
				return
			}
			if want == "flush" {
				_ = conn.WriteMessage(websocket.TextMessage, []byte(audioJSON))
				_ = conn.WriteJSON(event{Event: "final", IsFinal: true, Reason: flushedReason})
				_ = conn.WriteMessage(websocket.TextMessage, []byte(alignmentJSON))
			}
		}
		_ = conn.WriteMessage(websocket.TextMessage, []byte(finalJSON))
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := client.OpenDuplex(ctx, tts.DuplexRequest{
		Voice: fixtureVoice, Options: tts.Options{Provider: Options{EnableSubtitle: &on}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SendText(ctx, "你好"); err != nil {
		t.Fatal(err)
	}
	if err := s.(*duplex).Flush(ctx); err != nil {
		t.Fatal(err)
	}
	reader := s.(tts.EventReceiver)
	for _, want := range []tts.EventType{tts.EventAudio, tts.EventBatchCompleted} {
		e, err := reader.RecvEvent(ctx)
		if err != nil || e.Type != want {
			t.Fatalf("expected %s, got %+v, %v", want, e, err)
		}
	}
	e, err := reader.RecvEvent(ctx)
	checkAlignment(t, e, err)
	if err := s.CloseSend(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.RecvEvent(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestAudioReceiverIgnoresMetadataAndRetainsEOF(t *testing.T) {
	frames := []string{readyJSON, alignmentJSON, audioJSON, finalJSON}
	i := 0
	s := newStream(context.Background(), audio.Format{}, func() (event, error) {
		if i == len(frames) {
			return event{}, io.EOF
		}
		e, err := decodeEvent([]byte(frames[i]))
		i++
		return e, err
	}, func() {})
	defer s.Close()
	if chunk, err := s.Recv(context.Background()); err != nil || len(chunk.Data) != 3 {
		t.Fatalf("audio = %+v, %v", chunk, err)
	}
	if _, err := s.RecvEvent(context.Background()); !errors.Is(err, tts.ErrReceiveMode) {
		t.Fatalf("mixed receiver = %v", err)
	}
	if _, err := s.Recv(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
}
