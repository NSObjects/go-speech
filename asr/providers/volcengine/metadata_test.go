package volcengine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/gorilla/websocket"
)

func TestSpeakerLabels(t *testing.T) {
	for _, tc := range []struct {
		label string
		id    int
		valid bool
	}{
		{"0", 0, true}, {"3", 3, true}, {"-1", -1, true},
		{"Alice", 0, false}, {"01", 0, false}, {"-2", 0, false},
		{"999999999999999999999", 0, false}, {"", 0, false},
	} {
		raw, err := json.Marshal(tc.label)
		if err != nil {
			t.Fatal(err)
		}
		u := utterance{Text: "hello", Additions: map[string]json.RawMessage{"speaker_id": raw}}
		a := u.metadata()
		e := detailedEvent(asr.EventPartial, u.Text, a)
		d := a.Provider.(UtteranceDetails)
		if d.SpeakerLabel != tc.label || (a.SpeakerID != nil) != tc.valid ||
			(e.SpeakerID != nil) != tc.valid || tc.valid && (*a.SpeakerID != tc.id || *e.SpeakerID != tc.id) {
			t.Fatalf("speaker %q: %+v, %+v", tc.label, a, e)
		}
	}
}

func TestFinalMetadataUpdates(t *testing.T) {
	for _, mode := range []string{ResultFull, ResultSingle} {
		for _, details := range []bool{true, false} {
			s := &Stream{resultType: mode, finalSeen: make(map[string]struct{}), details: details}
			emit := func(raw string) {
				t.Helper()
				var r recognition
				if err := json.Unmarshal([]byte(raw), &r); err != nil {
					t.Fatal(err)
				}
				s.addEvents([]recognition{r}, false)
			}
			const first = `{"utterances":[{"text":"你好","definite":true,"start_time":0,"end_time":500}]}`
			const updated = `{"utterances":[{"text":"你好","definite":true,"start_time":0,"end_time":500,"additions":{"speaker_id":"0","emotion":"happy"},"words":[{"text":"你","start_time":0,"end_time":250}]}]}`
			emit(first)
			if len(s.queue) != 1 || s.queue[0].Type != asr.EventFinal || s.queue[0].Text != "你好" {
				t.Fatalf("initial final = %+v", s.queue)
			}
			var id string
			if details {
				id = s.queue[0].Utterance.ID
				if id == "" || s.queue[0].SpeakerID != nil {
					t.Fatal("unexpected initial metadata")
				}
			}
			s.queue = nil
			emit(first)
			if len(s.queue) != 0 {
				t.Fatal("identical snapshot emitted")
			}
			emit(updated)
			if details {
				if len(s.queue) != 1 {
					t.Fatalf("update = %+v", s.queue)
				}
				e := s.queue[0]
				if e.Type != asr.EventUtteranceUpdate || e.Text != "" || e.Utterance == nil ||
					e.Utterance.ID != id || e.Utterance.Text != "你好" || e.Utterance.Emotion != "happy" ||
					e.SpeakerID == nil || *e.SpeakerID != 0 || len(e.Utterance.Words) != 1 {
					t.Fatalf("update = %+v", e)
				}
				// Mutating a received snapshot cannot affect duplicate detection.
				e.Utterance.Provider.(UtteranceDetails).Additions["emotion"] = json.RawMessage(`"sad"`)
			} else if len(s.queue) != 0 {
				t.Fatal("metadata event without opt-in")
			}
			s.queue = nil
			emit(updated)
			emit(first)
			emit(`{"utterances":[{"text":"你好。","definite":true,"start_time":0,"end_time":500,"additions":{"speaker_id":"1"}}]}`)
			if len(s.queue) != 0 || s.confirmedText != "你好" {
				t.Fatalf("replay or text revision emitted: %+v", s.queue)
			}
			emit(`{"utterances":[{"text":"你好","definite":true,"start_time":600,"end_time":900,"additions":{"speaker_id":"1"}}]}`)
			if len(s.queue) != 1 || s.queue[0].Type != asr.EventFinal || s.queue[0].Text != "你好" {
				t.Fatal("distinct utterance lost")
			}
		}
	}
}

func TestUntimedMetadataNotCorrelated(t *testing.T) {
	s := &Stream{resultType: ResultFull, finalSeen: make(map[string]struct{}), details: true}
	for _, raw := range []string{
		`{"utterances":[{"text":"你好","definite":true,"end_time":500}]}`,
		`{"utterances":[{"text":"你好","definite":true,"end_time":500,"additions":{"speaker_id":"0"}}]}`,
	} {
		var r recognition
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			t.Fatal(err)
		}
		s.addEvents([]recognition{r}, false)
	}
	if len(s.queue) != 1 || s.queue[0].Utterance.ID != "" || len(s.finalMetadata) != 0 {
		t.Fatalf("unsafe correlation: %+v", s.queue)
	}
}

func TestSpeakerMetadataWireAndBatch(t *testing.T) {
	for _, batch := range []bool{false, true} {
		endpoint := mockEndpoint(t, func(ws *websocket.Conn, _ *http.Request) {
			_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
			if _, _, err := ws.ReadMessage(); err != nil {
				t.Error(err)
				return
			}
			for {
				_, frame, err := ws.ReadMessage()
				if err != nil {
					t.Error(err)
					return
				}
				_, flags, _, err := testRequestFrame(frame)
				if err != nil {
					t.Error(err)
					return
				}
				if flags&flagLast != 0 {
					break
				}
			}
			serverResult(t, ws, false, json.RawMessage(`{"result":{"utterances":[{"text":"你好","definite":true,"start_time":0,"end_time":500}]}}`))
			serverResult(t, ws, true, json.RawMessage(`{"result":{"utterances":[{"text":"你好","definite":true,"start_time":0,"end_time":500,"additions":{"speaker_id":"0","source":"two_pass"}},{"text":"再见","definite":true,"start_time":600,"end_time":900,"additions":{"speaker_id":"speaker-A"}}]}}`))
		})
		c, err := New(Config{APIKey: "test", Endpoint: endpoint})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		on := true
		cfg := asr.Config{
			Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
			Options: asr.Options{Provider: Options{EnableNonStream: &on, EnableSpeakerInfo: &on}},
		}
		if batch {
			out, err := c.Recognize(ctx, audio.Buffer{Format: cfg.Format, Data: make([]byte, 1280)}, cfg)
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			if out.Text != "你好再见" || len(out.Utterances) != 2 || out.Utterances[0].SpeakerID == nil ||
				*out.Utterances[0].SpeakerID != 0 || out.Utterances[1].SpeakerID != nil ||
				out.Utterances[1].Provider.(UtteranceDetails).SpeakerLabel != "speaker-A" {
				t.Fatalf("batch result = %+v", out)
			}
			continue
		}
		s, err := c.OpenStream(ctx, cfg)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		if err := s.Send(ctx, make([]byte, 1280)); err != nil {
			t.Fatal(err)
		}
		if err := s.CloseSend(ctx); err != nil {
			t.Fatal(err)
		}
		var id string
		for i, want := range []asr.EventType{asr.EventFinal, asr.EventUtteranceUpdate, asr.EventFinal} {
			e, err := s.Recv(ctx)
			if err != nil || e.Type != want || e.Utterance == nil {
				t.Fatalf("event %d: %+v, %v", i, e, err)
			}
			switch i {
			case 0:
				id = e.Utterance.ID
				if id == "" || e.SpeakerID != nil {
					t.Fatal("first final did not retain unknown speaker")
				}
			case 1:
				if e.Text != "" || e.Utterance.ID != id || e.SpeakerID == nil || *e.SpeakerID != 0 {
					t.Fatalf("update = %+v", e)
				}
			case 2:
				if e.Text != "再见" || e.SpeakerID != nil || e.Utterance.Provider.(UtteranceDetails).SpeakerLabel != "speaker-A" {
					t.Fatalf("native label = %+v", e)
				}
			}
		}
		for range 2 {
			if _, err := s.Recv(ctx); !errors.Is(err, io.EOF) {
				t.Fatalf("end = %v", err)
			}
		}
		_ = s.Close()
		cancel()
	}
}
