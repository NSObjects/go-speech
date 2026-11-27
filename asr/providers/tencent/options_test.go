package tencent

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

func TestAdvancedRecognitionWire(t *testing.T) {
	on, off := true, false
	wordInfo, emotion, domain, convert, modal := 2, 1, 1, 3, 2
	noise := 0.0
	c := testClient(t, func(ws *websocket.Conn, req *http.Request) {
		_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
		q := req.URL.Query()
		for key, want := range map[string]string{
			"needvad": "1", "hotword_list": "悟空|11,Eino|5",
			"customization_id": "custom", "replace_text_id": "replace",
			"filter_modal": "2", "filter_punc": "0", "filter_empty_result": "0",
			"convert_num_mode": "3", "word_info": "2", "noise_threshold": "0",
			"emotion_recognition": "1", "domain": "1",
		} {
			if q.Get(key) != want {
				t.Errorf("%s = %q, want %q", key, q.Get(key), want)
			}
		}
		if !verifySignature(req, "secret-key") {
			t.Error("advanced options missing from signature")
		}
		id := q.Get("voice_id")
		if err := sendResponse(ws, id, 0, 0, nil); err != nil {
			t.Error(err)
			return
		}
		for range 2 {
			if _, _, err := ws.ReadMessage(); err != nil {
				t.Error(err)
				return
			}
		}
		for _, raw := range []string{
			`{"slice_type":0,"index":0,"start_time":0}`,
			`{"slice_type":2,"index":0,"start_time":0,"end_time":300,"voice_text_str":"悟空","word_list":[{"word":"悟空","start_time":0,"end_time":300,"stable_flag":1}],"unknown_emotion_field":["happy"]}`,
			`{"slice_type":2,"index":0,"voice_text_str":"重复"}`,
			`{"slice_type":0,"index":1}`,
			`{"slice_type":2,"index":1,"voice_text_str":""}`,
		} {
			if err := ws.WriteJSON(map[string]any{"code": 0, "voice_id": id, "result": json.RawMessage(raw)}); err != nil {
				t.Error(err)
				return
			}
		}
		if err := sendResponse(ws, id, 0, 1, nil); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg := pcmConfig()
	cfg.Hints = []string{"悟空", "Eino"}
	cfg.Options.Provider = Options{
		Hotwords: []Hotword{{Word: "悟空", Weight: 11}},
		CustomizationID: "custom", ReplaceTextID: "replace",
		FilterModal: &modal, FilterPunctuation: &off, ReturnEmptyResults: &on,
		ConvertNumMode: &convert, WordInfo: &wordInfo, NoiseThreshold: &noise,
		EmotionRecognition: &emotion, Domain: &domain,
	}
	s, err := c.OpenStream(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Send(ctx, []byte{0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseSend(ctx); err != nil {
		t.Fatal(err)
	}
	for i, kind := range []asr.EventType{asr.EventSpeechStart, asr.EventFinal, asr.EventSpeechStart, asr.EventFinal} {
		e, err := s.Recv(ctx)
		if err != nil || e.Type != kind || e.Utterance == nil {
			t.Fatalf("event %d = %+v, %v", i, e, err)
		}
		if i == 1 {
			u := e.Utterance
			d := u.Provider.(UtteranceDetails)
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(d.RawResult, &raw); err != nil {
				t.Fatal(err)
			}
			if e.Text != "悟空" || u.StartTime == nil || *u.StartTime != 0 ||
				u.EndTime == nil || *u.EndTime != 300*time.Millisecond || len(u.Words) != 1 ||
				len(d.StableFlags) != 1 || d.StableFlags[0] == nil || *d.StableFlags[0] != 1 ||
				string(raw["unknown_emotion_field"]) != `["happy"]` || u.Emotion != "" {
				t.Fatalf("metadata = %+v", u)
			}
		}
		if i == 3 && e.Text != "" {
			t.Fatal("empty final lost")
		}
	}
	if _, err := s.Recv(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("end = %v", err)
	}
}

func TestSpeakerContextBatch(t *testing.T) {
	on := true
	strategy := 0
	c := testClient(t, func(ws *websocket.Conn, req *http.Request) {
		q := req.URL.Query()
		if q.Get("enable_speaker_context") != "1" || q.Get("speaker_context_id") != "previous" ||
			q.Get("sentence_strategy") != "0" || !verifySignature(req, "secret-key") {
			t.Error("invalid speaker context query")
		}
		id := q.Get("voice_id")
		_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
		if err := sendResponse(ws, id, 0, 0, nil); err != nil {
			t.Error(err)
			return
		}
		for range 2 {
			if _, _, err := ws.ReadMessage(); err != nil {
				t.Error(err)
				return
			}
		}
		for _, message := range []map[string]any{
			{"sentences": json.RawMessage(`{"sentence_list":[{"sentence_id":0,"sentence_type":1,"speaker_id":0,"sentence":"你好","start_time":0,"end_time":250}]}`)},
			{"final": 1, "speaker_context_id": "next"},
		} {
			message["voice_id"], message["code"] = id, 0
			if err := ws.WriteJSON(message); err != nil {
				t.Error(err)
				return
			}
		}
	})
	c.cfg.EngineModel = "16k_zh_en_speaker_2.0"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg := pcmConfig()
	cfg.Options = asr.Options{UtteranceDetails: true, Provider: Options{
		EnableSpeakerContext: &on, SpeakerContextID: "previous", SentenceStrategy: &strategy,
	}}
	out, err := c.Recognize(ctx, audio.Buffer{Format: cfg.Format, Data: make([]byte, 1280)}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if out.Text != "你好" || len(out.Utterances) != 1 {
		t.Fatalf("result = %+v", out)
	}
	d, ok := out.Provider.(SessionDetails)
	u := out.Utterances[0]
	ud := u.Provider.(UtteranceDetails)
	if !ok || d.SpeakerContextID != "next" || ud.SpeakerID == nil || *ud.SpeakerID != 0 ||
		u.EndTime == nil || *u.EndTime != 250*time.Millisecond {
		t.Fatalf("result metadata = %+v", out)
	}
}

func TestAdvancedOptionValidation(t *testing.T) {
	on := true
	one, special, bad := 1, 100, 4
	c := Config{EngineModel: "16k_zh"}
	for _, o := range []Options{
		{EmotionRecognition: &one},
		{EnableSpeakerContext: &on},
		{SentenceStrategy: &one},
		{WordInfo: &special},
		{ConvertNumMode: &bad},
		{Hotwords: []Hotword{{Word: "bad word", Weight: 5}}},
	} {
		if _, _, err := c.requestOptions(asr.Config{}, o); err == nil {
			t.Errorf("invalid options accepted: %+v", o)
		}
	}
	c.EngineModel = "16k_zh_en_2.0"
	if _, _, err := c.requestOptions(asr.Config{}, Options{WordInfo: &one}); err == nil {
		t.Fatal("undocumented V2 word timing accepted")
	}
	if _, _, err := c.requestOptions(asr.Config{}, Options{EmotionRecognition: &one}); err == nil {
		t.Fatal("undocumented V2 emotion accepted")
	}
}
