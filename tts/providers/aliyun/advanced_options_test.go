package aliyun

import (
	"encoding/json"
	"testing"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/tts"
)

func TestAdvancedVoiceOptions(t *testing.T) {
	on, off := true, false
	o := Options{
		BitRateKbps: 64, LanguageHint: "ja", EnableMarkdownFilter: &off,
		EnableAIGCTag: &on, AIGCPropagator: "fixture", AIGCPropagateID: "id",
	}
	req := tts.Request{
		Format:  audio.Format{Encoding: audio.EncodingMP3},
		Options: tts.Options{Provider: o},
	}
	p, err := voiceParameters(req.Options)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateVoiceOptions(req, ModelCosyVoiceV3Flash, p); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"aigc_propagate_id":"id","aigc_propagator":"fixture","bit_rate":64,"enable_aigc_tag":true,"enable_markdown_filter":false,"language_hints":["ja"]}`
	if string(b) != want {
		t.Fatalf("parameters = %s", b)
	}
	for _, tc := range []struct {
		options  Options
		encoding audio.Encoding
		model    string
	}{
		{Options{BitRateKbps: 5}, audio.EncodingMP3, ModelCosyVoiceV3Flash},
		{Options{BitRateKbps: 64}, audio.EncodingPCM16LE, ModelCosyVoiceV3Flash},
		{Options{EnableMarkdownFilter: &on}, audio.EncodingMP3, "cosyvoice-v2"},
		{Options{EnableAIGCTag: &on}, audio.EncodingPCM16LE, ModelCosyVoiceV3Flash},
		{Options{EnableAIGCTag: &on}, audio.EncodingMP3, "cosyvoice-v3.5-flash"},
		{Options{AIGCPropagator: "fixture"}, audio.EncodingMP3, ModelCosyVoiceV3Flash},
		{Options{LanguageHint: "invalid"}, audio.EncodingMP3, ModelCosyVoiceV3Flash},
	} {
		req.Format.Encoding = tc.encoding
		req.Options.Provider = tc.options
		params, err := voiceParameters(req.Options)
		if err == nil {
			err = validateVoiceOptions(req, tc.model, params)
		}
		if err == nil {
			t.Errorf("invalid combination accepted: %+v", tc)
		}
	}
}
