package speech_test

import (
	"slices"
	"testing"

	speech "github.com/NSObjects/go-speech"
	"github.com/NSObjects/go-speech/asr"
	aliasr "github.com/NSObjects/go-speech/asr/providers/aliyun"
	tenasr "github.com/NSObjects/go-speech/asr/providers/tencent"
	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/capability"
	"github.com/NSObjects/go-speech/tts"
	alitts "github.com/NSObjects/go-speech/tts/providers/aliyun"
	tentts "github.com/NSObjects/go-speech/tts/providers/tencent"
	volctts "github.com/NSObjects/go-speech/tts/providers/volcengine"
)

func TestASRFeatureModels(t *testing.T) {
	on := true
	for _, tc := range []struct {
		model     string
		emotion   capability.Status
		semantic  bool
		context   bool
		dialect   bool
	}{
		{aliasr.ModelQwenAudio31Streaming, capability.Unsupported, false, true, true},
		{aliasr.ModelQwenAudio31Message, capability.Unsupported, false, true, true},
		{aliasr.ModelFunASRRealtime, capability.Unsupported, false, true, false},
		{aliasr.ModelParaformerRealtime, capability.Unsupported, true, false, false},
		{aliasr.ModelParaformerRealtime8K, capability.Supported, true, false, false},
	} {
		t.Run(tc.model, func(t *testing.T) {
			c, err := speech.NewASR(speech.AliyunASR{APIKey: "test", Model: tc.model})
			if err != nil {
				t.Fatal(err)
			}
			f := c.(asr.CapabilityProvider).Capabilities().Features
			if f.Get(asr.FeatureEmotion).Status != tc.emotion || f.Supports(asr.FeatureSemanticSegmentation) != tc.semantic ||
				f.Supports(asr.FeatureContext) != tc.context || f.Supports(asr.FeatureDialect) != tc.dialect {
				t.Fatalf("features = %+v", f)
			}
			rate := 16000
			if tc.model == aliasr.ModelParaformerRealtime8K {
				rate = 8000
			}
			cfg := asr.Config{Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: rate, Channels: 1}}
			cfg.Options.Provider = aliasr.Options{SemanticPunctuation: &on}
			if err := c.(asr.ConfigValidator).ValidateConfig(cfg); (err == nil) != tc.semantic {
				t.Fatalf("semantic support differs from validator: %v", err)
			}
		})
	}
}

func TestTencentFeatureModes(t *testing.T) {
	for _, tc := range []struct {
		engine  string
		emotion capability.Status
		speaker bool
		modes   []int
	}{
		{"16k_zh", capability.Unknown, false, []int{0, 1, 2}},
		{"16k_zh_en", capability.Unknown, false, []int{0, 1, 2, 100}},
		{"16k_zh_en_2.0", capability.Unsupported, false, nil},
		{"16k_zh_en_speaker_2.0", capability.Unsupported, true, nil},
	} {
		t.Run(tc.engine, func(t *testing.T) {
			c, err := speech.NewASR(speech.TencentASR{AppID: "123", SecretID: "id", SecretKey: "key", EngineModel: tc.engine, NeedVAD: true})
			if err != nil {
				t.Fatal(err)
			}
			f := c.(asr.CapabilityProvider).Capabilities().Features
			if f.Get(asr.FeatureEmotion).Status != tc.emotion ||
				f.Supports(asr.FeatureSpeakerContext) != tc.speaker ||
				f.Supports(asr.FeatureWordTimestamps) != (len(tc.modes) != 0) ||
				!slices.Equal(f.Get(asr.FeatureWordTimestamps).Parameters[0].IntValues, tc.modes) {
				t.Fatalf("features = %+v", f)
			}
			for _, mode := range []int{0, 1, 2, 100} {
				cfg := asr.Config{Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
					Options: asr.Options{Provider: tenasr.Options{WordInfo: &mode}}}
				if err := c.(asr.ConfigValidator).ValidateConfig(cfg); (err == nil) != slices.Contains(tc.modes, mode) {
					t.Fatalf("mode %d: %v", mode, err)
				}
			}
		})
	}
}

func TestTTSFeatureRanges(t *testing.T) {
	for _, tc := range []struct {
		name    string
		config  speech.TTSConfig
		voice   string
		unit    string
		min     float64
		max     float64
		options func(*float64) tts.Options
	}{
		{"volcengine", speech.VolcengineTTS{APIKey: "test"}, "voice", "multiplier", 0.5, 2, func(v *float64) tts.Options { return tts.Options{Speed: v} }},
		{"aliyun", speech.AliyunTTS{APIKey: "test"}, "voice", "multiplier", 0.5, 2, func(v *float64) tts.Options { return tts.Options{Speed: v} }},
		{"tencent", speech.TencentTTS{AppID: "123", SecretID: "id", SecretKey: "key"}, "101001", "native", -2, 6, func(v *float64) tts.Options { return tts.Options{Provider: tentts.Options{Speed: v}} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := speech.NewTTS(tc.config)
			if err != nil {
				t.Fatal(err)
			}
			cp := c.(tts.CapabilityProvider)
			f := cp.Capabilities().Features
			s := f.Get(tts.FeatureSpeed)
			if s.Status != capability.Supported || len(s.Parameters) != 1 ||
				s.Parameters[0].Unit != tc.unit || s.Parameters[0].Range == nil ||
				s.Parameters[0].Range.Min != tc.min || s.Parameters[0].Range.Max != tc.max {
				t.Fatalf("speed = %+v", s)
			}
			r := s.Parameters[0].Range
			for _, v := range []float64{r.Min - 0.1, r.Min, r.Max, r.Max + 0.1} {
				req := tts.Request{Voice: tc.voice, Text: "hello", Options: tc.options(&v)}
				want := v >= r.Min && v <= r.Max
				if err := c.(tts.RequestValidator).ValidateRequest(req); (err == nil) != want {
					t.Fatalf("range mismatch for %v: %v", v, err)
				}
			}
			r.Min = 999
			delete(f, tts.FeatureEmotion)
			if cp.Capabilities().Features.Get(tts.FeatureSpeed).Parameters[0].Range.Min != tc.min {
				t.Fatal("mutable feature range shared")
			}
			if f.Get("unknown_future_feature").Status != capability.Unknown || f.Supports("unknown_future_feature") {
				t.Fatal("unknown feature claimed supported")
			}
		})
	}
}

func TestTTSFeatureEnumsAndRestrictions(t *testing.T) {
	for _, model := range []string{"cosyvoice-v2", "cosyvoice-v3-flash", "custom"} {
		c, err := speech.NewTTS(speech.AliyunTTS{APIKey: "test", Model: model})
		if err != nil {
			t.Fatal(err)
		}
		f := c.(tts.CapabilityProvider).Capabilities().Features
		on := true
		req := tts.Request{Voice: "voice", Text: "hello", Options: tts.Options{Provider: alitts.Options{EnableAIGCTag: &on}}}
		if err := c.(tts.RequestValidator).ValidateRequest(req); (err == nil) != f.Supports(tts.FeatureWatermark) {
			t.Fatalf("%s watermark feature differs: %v", model, err)
		}
		languages := f.Get(tts.FeatureLanguage).Parameters[0].StringValues
		for _, lang := range append(languages, "invalid") {
			req.Options.Provider = alitts.Options{LanguageHint: lang}
			if err := c.(tts.RequestValidator).ValidateRequest(req); (err == nil) != slices.Contains(languages, lang) {
				t.Fatalf("language %s: %v", lang, err)
			}
		}
	}
	c, err := speech.NewTTS(speech.VolcengineTTS{APIKey: "test"})
	if err != nil {
		t.Fatal(err)
	}
	values := c.(tts.CapabilityProvider).Capabilities().Features.Get(tts.FeatureBitRate).Parameters[0].IntValues
	for _, rate := range append(values, 96000) {
		req := tts.Request{Voice: "voice", Text: "hello", Options: tts.Options{Provider: volctts.Options{BitRate: rate}}}
		if err := c.(tts.RequestValidator).ValidateRequest(req); (err == nil) != slices.Contains(values, rate) {
			t.Fatalf("bit rate %d: %v", rate, err)
		}
	}
}
