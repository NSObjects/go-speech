package volcengine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/gorilla/websocket"
)

func TestCorpusAndEndpointWire(t *testing.T) {
	on, off := true, false
	zero, segment, force := time.Duration(0), 3*time.Second, time.Second
	endpoint := mockEndpoint(t, func(ws *websocket.Conn, _ *http.Request) {
		_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, frame, err := ws.ReadMessage()
		if err != nil {
			t.Error(err)
			return
		}
		_, _, raw, err := testRequestFrame(frame)
		if err != nil {
			t.Error(err)
			return
		}
		var request struct {
			Request map[string]any `json:"request"`
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Error(err)
			return
		}
		r := request.Request
		for key, want := range map[string]any{
			"enable_nonstream": true, "vad_segment_duration": float64(3000),
			"force_to_speech_time": float64(1000), "enable_poi_fc": true,
			"enable_music_fc": false,
		} {
			if r[key] != want {
				t.Errorf("%s = %v, want %v", key, r[key], want)
			}
		}
		if _, ok := r["end_window_size"]; ok {
			t.Error("constructor end window was not cleared")
		}
		corpus, ok := r["corpus"].(map[string]any)
		if !ok {
			t.Error("missing corpus")
			return
		}
		for key, want := range map[string]string{
			"boosting_table_id": "hot-id", "boosting_table_name": "hot-name",
			"correct_table_id": "correct-id", "correct_table_name": "correct-name",
			"regex_correct_table_id": "regex-id", "regex_correct_table_name": "regex-name",
		} {
			if corpus[key] != want {
				t.Errorf("%s = %v, want %v", key, corpus[key], want)
			}
		}
		var got contextPayload
		ctxJSON, ok := corpus["context"].(string)
		if !ok {
			t.Error("context must be a JSON string")
			return
		}
		if err := json.Unmarshal([]byte(ctxJSON), &got); err != nil {
			t.Error(err)
			return
		}
		want := contextPayload{
			Hotwords:    []contextHotword{{Word: "黑神话悟空"}, {Word: "Eino"}},
			ContextType: "dialog_ctx",
			ContextData: []ContextItem{{Speaker: "user", Text: "我在玩游戏"}, {ImageURL: "https://example.com/scene.png"}, {Speaker: "bot", Text: "看到画面了"}},
			Location:    &contextLocation{CityName: "上海市"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("context = %+v, want %+v", got, want)
		}
		filter, ok := r["sensitive_words_filter"].(string)
		if !ok || filter != `{"system_reserved_filter":false,"filter_with_empty":["remove"],"filter_with_signed":["mask"]}` {
			t.Errorf("filter = %v", r["sensitive_words_filter"])
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
		serverResult(t, ws, true, json.RawMessage(`{"result":{"text":"你好","utterances":[{"text":"你好","definite":true}]}}`))
	})
	c, err := New(Config{APIKey: "test", Endpoint: endpoint, EndWindowSize: 800 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg := asr.Config{
		Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
		Hints:  []string{" 黑神话悟空 ", "Eino"},
		Options: asr.Options{Provider: Options{
			EnableNonStream: &on, EndWindowSize: &zero, VADSegmentDuration: &segment,
			ForceToSpeechTime: &force, EnablePOIFC: &on, EnableMusicFC: &off,
			Corpus: &Corpus{
				BoostingTableID: "hot-id", BoostingTableName: "hot-name",
				CorrectTableID: "correct-id", CorrectTableName: "correct-name",
				RegexCorrectTableID: "regex-id", RegexCorrectTableName: "regex-name",
				Context: []ContextItem{{Speaker: "user", Text: "我在玩游戏"}, {ImageURL: "https://example.com/scene.png"}, {Speaker: "bot", Text: "看到画面了"}},
				CityName: "上海市",
			},
			SensitiveWords: &SensitiveWordsFilter{SystemReservedFilter: &off, FilterWithEmpty: []string{"remove"}, FilterWithSigned: []string{"mask"}},
		}},
	}
	s, err := c.OpenStream(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Send(ctx, make([]byte, 1280)); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseSend(ctx); err != nil {
		t.Fatal(err)
	}
	e, err := s.Recv(ctx)
	if err != nil || e.Type != asr.EventFinal || e.Text != "你好" {
		t.Fatalf("event = %+v, %v", e, err)
	}
	if _, err := s.Recv(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("end = %v", err)
	}
	if c.cfg.EndWindowSize != 800*time.Millisecond || c.cfg.EnableNonStream {
		t.Fatal("request changed constructor defaults")
	}
}

func TestCorpusValidation(t *testing.T) {
	on := true
	segment, negative, fractional := time.Second, -time.Millisecond, time.Microsecond
	c := Config{ResultType: ResultFull}
	image := ContextItem{ImageURL: "https://example.com/scene.png"}
	for _, tc := range []struct {
		name string
		o    Options
	}{
		{"poi without second pass", Options{EnablePOIFC: &on}},
		{"music without second pass", Options{EnableMusicFC: &on}},
		{"negative force", Options{ForceToSpeechTime: &negative}},
		{"fractional segment", Options{VADSegmentDuration: &fractional}},
		{"invalid filter", Options{SensitiveWords: &SensitiveWordsFilter{FilterWithEmpty: []string{""}}}},
		{"image without second pass", Options{Corpus: &Corpus{Context: []ContextItem{image}}}},
		{"two images", Options{EnableNonStream: &on, Corpus: &Corpus{Context: []ContextItem{image, image}}}},
		{"invalid image URL", Options{EnableNonStream: &on, Corpus: &Corpus{Context: []ContextItem{{ImageURL: "file:///tmp/image.png"}}}}},
		{"invalid role", Options{Corpus: &Corpus{Context: []ContextItem{{Speaker: "assistant", Text: "hello"}}}}},
		{"empty item", Options{Corpus: &Corpus{Context: []ContextItem{{}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := make(map[string]any)
			cfg := asr.Config{Options: asr.Options{Provider: tc.o}}
			_, _, err := c.applyOptions(cfg.Options, req)
			if err == nil {
				err = applyCorpus(cfg, req)
			}
			if err == nil {
				t.Fatal("invalid option accepted")
			}
		})
	}
	c.EndWindowSize = 800 * time.Millisecond
	if _, _, err := c.applyOptions(asr.Options{Provider: Options{VADSegmentDuration: &segment}}, make(map[string]any)); err == nil {
		t.Fatal("conflicting endpoint settings accepted")
	}
}

func TestCorpusDefaultsAndHints(t *testing.T) {
	req := make(map[string]any)
	if err := applyCorpus(asr.Config{}, req); err != nil || len(req) != 0 {
		t.Fatalf("default corpus = %v, %v", req, err)
	}
	if err := applyCorpus(asr.Config{Hints: []string{" Eino "}}, req); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"context": `{"hotwords":[{"word":"Eino"}]}`}
	if !reflect.DeepEqual(req["corpus"], want) {
		t.Fatalf("hints-only corpus = %v", req["corpus"])
	}
}
