package aliyun

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/gorilla/websocket"
)

func TestAdvancedModelParameters(t *testing.T) {
	on, off := true, false
	zero := 0
	noise := 0.0
	format := audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1}
	c := Config{Model: ModelQwenAudio31Streaming, Heartbeat: true, KeepDialect: true, MaxSentenceSilenceMs: 800, Vocabulary: map[string]int{"old": 3}}
	cfg := asr.Config{
		Format: format,
		Hints:  []string{"Eino"},
		Options: asr.Options{Provider: Options{
			Heartbeat: &off, KeepDialect: &off, MaxSentenceSilenceMs: &zero,
			Vocabulary: map[string]int{"新词": 50}, LanguageHints: []string{"zh", "en"},
			MultiThresholdMode: &on, SpeechNoiseThreshold: &noise,
			SensitiveWords: &SensitiveWordsFilter{SystemReservedFilter: &off, FilterWithSigned: []string{"mask"}},
		}},
	}
	p, err := c.parameters(cfg, "pcm")
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"heartbeat": false, "keep_dialect": false, "multi_threshold_mode_enabled": true,
		"speech_noise_threshold": 0.0, "language_hints": []string{"zh", "en"},
		"vocabulary": map[string]int{"Eino": 3, "新词": 50},
		"special_word_filter": `{"filter_with_signed":{"word_list":["mask"]},"system_reserved_filter":false}`,
	} {
		if !reflect.DeepEqual(p[key], want) {
			t.Errorf("%s = %#v, want %#v", key, p[key], want)
		}
	}
	if _, ok := p["max_sentence_silence"]; ok {
		t.Error("silence override did not clear default")
	}
	if !c.Heartbeat || !c.KeepDialect || c.MaxSentenceSilenceMs != 800 || c.Vocabulary["old"] != 3 {
		t.Fatal("request mutated constructor defaults")
	}
	for _, tc := range []struct {
		model string
		opts  asr.Options
	}{
		{ModelQwenAudio31Streaming, asr.Options{Punctuation: &off}},
		{ModelFunASRRealtime, asr.Options{DisfluencyRemoval: &off}},
		{ModelQwenAudio31Message, asr.Options{Provider: Options{MultiThresholdMode: &on}}},
		{ModelParaformerRealtime, asr.Options{Provider: Options{SpeechNoiseThreshold: &noise}}},
		{ModelQwenAudio31Message, asr.Options{Provider: Options{SensitiveWords: &SensitiveWordsFilter{}}}},
	} {
		if _, err := (Config{Model: tc.model}).parameters(asr.Config{Format: format, Options: tc.opts}, "pcm"); !speecherr.IsCode(err, speecherr.CodeUnsupported) {
			t.Errorf("%s unsupported option: %v", tc.model, err)
		}
	}
	para := Config{Model: ModelParaformerRealtime, DisfluencyRemoval: true}
	p, err = para.parameters(asr.Config{Format: format, Options: asr.Options{
		Punctuation: &off, TextNormalization: &off, DisfluencyRemoval: &off,
		Provider: Options{SemanticPunctuation: &on},
	}}, "pcm")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"punctuation_prediction_enabled", "inverse_text_normalization_enabled", "disfluency_removal_enabled"} {
		if p[key] != false {
			t.Errorf("%s = %v", key, p[key])
		}
	}
	if p["semantic_punctuation_enabled"] != true {
		t.Fatal("semantic punctuation missing")
	}
	para.MaxSentenceSilenceMs = 800
	if _, err := para.parameters(asr.Config{Format: format, Options: asr.Options{Provider: Options{SemanticPunctuation: &on}}}, "pcm"); err == nil {
		t.Fatal("semantic and explicit silence accepted")
	}
}

func TestContextInputValidation(t *testing.T) {
	c := Config{Model: ModelQwenAudio31Streaming}
	for _, messages := range [][]ContextMessage{
		{{Role: "assistant", Text: "first"}},
		{{Role: "user", Text: strings.Repeat("你", 401)}},
		{{Role: "user", Text: "hello"}, {Role: "assistant", Text: strings.Repeat("你", 400)}},
		{{Role: "user", Text: " "}},
	} {
		if _, err := c.contextInput(messages); err == nil {
			t.Errorf("invalid context accepted: %+v", messages)
		}
	}
	if _, err := (Config{Model: ModelParaformerRealtime}).contextInput([]ContextMessage{{Role: "user", Text: "hello"}}); !speecherr.IsCode(err, speecherr.CodeUnsupported) {
		t.Fatalf("Paraformer context: %v", err)
	}
}

func TestAdvancedRecognitionWire(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := websocket.Upgrader{}
		conn, err := u.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		var cmd taskMessage
		if err := conn.ReadJSON(&cmd); err != nil {
			t.Error(err)
			return
		}
		b, err := json.Marshal(cmd.Payload.Input["context"])
		if err != nil || string(b) != `[{"content":[{"text":"识别 Eino","type":"input_text"}],"role":"user"},{"content":[{"text":"明白","type":"text"}],"role":"assistant"}]` {
			t.Errorf("context = %s, %v", b, err)
		}
		if cmd.Payload.Parameters["heartbeat"] != false || cmd.Payload.Parameters["keep_dialect"] != true {
			t.Errorf("parameters = %+v", cmd.Payload.Parameters)
		}
		send := func(event string, sentence any) {
			if err := conn.WriteJSON(map[string]any{
				"header": map[string]any{"event": event, "task_id": cmd.Header.TaskID},
				"payload": map[string]any{"output": map[string]any{"sentence": sentence}},
			}); err != nil {
				t.Error(err)
			}
		}
		send("task-started", nil)
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Error(err)
			return
		}
		send("result-generated", json.RawMessage(`{"sentence_id":0,"heartbeat":true}`))
		send("result-generated", json.RawMessage(`{"sentence_id":1,"sentence_begin":true,"begin_time":0,"end_time":null}`))
		send("result-generated", json.RawMessage(`{"sentence_id":1,"text":"E","begin_time":0,"end_time":null,"words":[{"text":"E","begin_time":0,"end_time":0,"fixed":false}]}`))
		send("result-generated", json.RawMessage(`{"sentence_id":1,"sentence_end":true,"text":"Eino。","begin_time":0,"end_time":250,"words":[{"text":"Eino","begin_time":0,"end_time":250,"punctuation":"。","fixed":true}]}`))
		if err := conn.ReadJSON(&cmd); err != nil {
			t.Error(err)
			return
		}
		send("task-finished", nil)
	}))
	defer server.Close()
	c, err := New(Config{APIKey: "test", Endpoint: "ws" + strings.TrimPrefix(server.URL, "http") + "/api-ws/v1/inference", Heartbeat: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	on, off := true, false
	s, err := c.OpenStream(ctx, asr.Config{
		Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
		Options: asr.Options{UtteranceDetails: true, Provider: Options{
			Heartbeat: &off, KeepDialect: &on,
			Context: []ContextMessage{{Role: "user", Text: "识别 Eino"}, {Role: "assistant", Text: "明白"}},
		}},
	})
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
	for _, kind := range []asr.EventType{asr.EventSpeechStart, asr.EventPartial, asr.EventFinal} {
		e, err := s.Recv(ctx)
		if err != nil || e.Type != kind || e.Utterance == nil {
			t.Fatalf("event = %+v, %v", e, err)
		}
		u := e.Utterance
		d := u.Provider.(UtteranceDetails)
		if d.SentenceID == nil || *d.SentenceID != 1 || u.StartTime == nil || *u.StartTime != 0 {
			t.Fatalf("missing identity or timing: %+v", u)
		}
		if kind != asr.EventFinal && u.EndTime != nil {
			t.Fatal("null timestamp became a value")
		}
		if kind == asr.EventFinal && (!u.Definite || u.EndTime == nil || *u.EndTime != 250*time.Millisecond || len(u.Words) != 1 || len(d.Words) != 1 || d.Words[0].Punctuation != "。" || d.Words[0].Fixed == nil || !*d.Words[0].Fixed) {
			t.Fatalf("final metadata = %+v", u)
		}
	}
	if _, err := s.Recv(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("end = %v", err)
	}
}

func TestParaformerEmotionDetails(t *testing.T) {
	var sentence responseSentence
	if err := json.Unmarshal([]byte(`{"text":"你好","sentence_end":true,"emo_tag":"neutral","emo_confidence":0,"begin_time":0,"end_time":null}`), &sentence); err != nil {
		t.Fatal(err)
	}
	s := &Stream{details: true}
	e := s.sentenceEvent(asr.EventFinal, sentence)
	d := e.Utterance.Provider.(UtteranceDetails)
	if e.Utterance.Emotion != "neutral" || d.EmotionConfidence == nil || *d.EmotionConfidence != 0 || e.Utterance.EndTime != nil {
		t.Fatalf("emotion metadata = %+v", e.Utterance)
	}
	sentence.SentenceEnd = false
	e = s.sentenceEvent(asr.EventPartial, sentence)
	if e.Utterance.Emotion != "" || e.Utterance.Provider.(UtteranceDetails).EmotionConfidence != nil {
		t.Fatal("partial emotion should not be exposed")
	}
}
