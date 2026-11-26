package volcengine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/tts"
	"github.com/gorilla/websocket"
)

func TestAdvancedHTTPAndDuplexRequests(t *testing.T) {
	no, yes, silence := false, true, 250.0
	native := Options{
		SessionID: "conversation-fixture", LanguageCode: "zh",
		BitRate: 160000, Instructions: []string{"温柔", "自然"},
		PronunciationTone: []string{"北京/(bei3)(jing1)", "omg/oh my god"},
		TextNormalization: &no, EnableMarkdownFilter: &yes,
		Watermark: &Watermark{
			AIGCWatermark: &no,
			AIGCMetadata: &AIGCMetadata{Enable: &yes, ProduceID: "fixture"},
		},
	}
	assert := func(raw map[string]json.RawMessage) {
		t.Helper()
		for key, want := range map[string]string{
			"session_id": `"conversation-fixture"`,
			"language_code": `"zh"`,
			"text_normalization": "false",
			"enable_markdown_filter": "true",
			"pronunciation_dictionary": `{"tone":["北京/(bei3)(jing1)","omg/oh my god"]}`,
			"voice_settings": `{"instructions":["温柔","自然"]}`,
			"watermark": `{"aigc_watermark":false,"aigc_metadata":{"enable":true,"produce_id":"fixture"}}`,
		} {
			if string(raw[key]) != want {
				t.Errorf("%s = %s, want %s", key, raw[key], want)
			}
		}
		var output outputFormat
		if err := json.Unmarshal(raw["output_format"], &output); err != nil || output.BitRate != 160000 {
			t.Errorf("output format = %+v, %v", output, err)
		}
	}
	upgrader := websocket.Upgrader{}
	client := fixtureClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !websocket.IsWebSocketUpgrade(r) {
			var raw map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
				t.Error(err)
				return
			}
			assert(raw)
			if string(raw["silence_duration"]) != "250" {
				t.Errorf("silence_duration = %s", raw["silence_duration"])
			}
			_, _ = fmt.Fprintln(w, readyJSON)
			_, _ = fmt.Fprintln(w, audioJSON)
			_, _ = fmt.Fprintln(w, finalJSON)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		var raw map[string]json.RawMessage
		if err := conn.ReadJSON(&raw); err != nil {
			t.Error(err)
			return
		}
		assert(raw)
		if _, ok := raw["silence_duration"]; ok {
			t.Error("HTTP-only silence sent over WebSocket")
		}
		if err := conn.WriteJSON(event{Event: "ready", SessionID: native.SessionID}); err != nil {
			t.Error(err)
			return
		}
		for _, want := range []string{"text", "close"} {
			var in map[string]json.RawMessage
			if err := conn.ReadJSON(&in); err != nil {
				t.Error(err)
				return
			}
			if string(in["event"]) != fmt.Sprintf("%q", want) || len(in) > 2 {
				t.Errorf("unexpected event: %v", in)
			}
		}
		_ = conn.WriteJSON(event{Event: "audio", Audio: []byte{1, 2, 3}})
		_ = conn.WriteJSON(event{Event: "final", IsFinal: true, Reason: closedReason})
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpOptions := native
	httpOptions.SilenceDurationMS = &silence
	if _, err := client.Synthesize(ctx, tts.Request{
		Text: "你好", Voice: fixtureVoice, Options: tts.Options{Provider: httpOptions},
	}); err != nil {
		t.Fatal(err)
	}
	s, err := client.OpenDuplex(ctx, tts.DuplexRequest{
		Voice: fixtureVoice, Options: tts.Options{Provider: native},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SendText(ctx, "你好"); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseSend(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Recv(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestAdvancedOptionsValidation(t *testing.T) {
	silence := 1.0
	for _, tc := range []struct {
		native Options
		output outputFormat
		duplex bool
	}{
		{Options{BitRate: 160000}, outputFormat{Format: "mp3", SampleRate: 8000}, false},
		{Options{BitRate: 64000}, outputFormat{Format: "pcm"}, false},
		{Options{BitRate: 96000}, outputFormat{Format: "ogg_opus"}, false},
		{Options{PronunciationTone: []string{"invalid"}}, outputFormat{}, false},
		{Options{SilenceDurationMS: &silence}, outputFormat{}, true},
	} {
		if _, err := advanced(tc.native, &tc.output, tc.duplex); err == nil {
			t.Errorf("invalid options accepted: %+v", tc)
		}
	}
	if _, err := settings(tts.Options{Instruction: "温柔", Provider: Options{Instructions: []string{"自然"}}}); err == nil {
		t.Error("conflicting instruction sources accepted")
	}
}
