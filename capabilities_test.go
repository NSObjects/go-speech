package speech_test

import (
	"context"
	"errors"
	"net"
	"slices"
	"testing"

	speech "github.com/NSObjects/go-speech"
	"github.com/NSObjects/go-speech/asr"
	aliasr "github.com/NSObjects/go-speech/asr/providers/aliyun"
	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/NSObjects/go-speech/tts"
	alitts "github.com/NSObjects/go-speech/tts/providers/aliyun"
	tentts "github.com/NSObjects/go-speech/tts/providers/tencent"
	volctts "github.com/NSObjects/go-speech/tts/providers/volcengine"
	"github.com/gorilla/websocket"
)

func TestOfflineASRCapabilities(t *testing.T) {
	dialer := &websocket.Dialer{NetDialContext: func(context.Context, string, string) (net.Conn, error) {
		t.Error("offline validation opened a network connection")
		return nil, errors.New("unexpected dial")
	}}
	for _, tc := range []struct {
		provider string
		cfg      speech.ASRConfig
		rate     int
	}{
		{"volcengine", speech.VolcengineASR{APIKey: "test", Dialer: dialer}, 16000},
		{"aliyun", speech.AliyunASR{APIKey: "test", Model: aliasr.ModelParaformerRealtime8K, Dialer: dialer}, 8000},
		{"tencent", speech.TencentASR{AppID: "123", SecretID: "id", SecretKey: "key", InputSampleRate: 8000, Dialer: dialer}, 8000},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			c, err := speech.NewASR(tc.cfg)
			if err != nil {
				t.Fatal(err)
			}
			cap := c.(asr.CapabilityProvider).Capabilities()
			if cap.Provider != tc.provider || cap.Model == "" || cap.Protocol == "" || !cap.Batch || !cap.Streaming {
				t.Fatalf("capabilities = %+v", cap)
			}
			cfg := asr.Config{Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: tc.rate, Channels: 1}}
			v := c.(asr.ConfigValidator)
			if !cap.Audio.Supports(cfg.Format) || v.ValidateConfig(cfg) != nil {
				t.Fatal("valid audio rejected")
			}
			cap.Audio.SampleRates[0] = 1
			if !c.(asr.CapabilityProvider).Capabilities().Audio.Supports(cfg.Format) {
				t.Fatal("capabilities leaked mutable state")
			}
			cfg.Format.SampleRate = 44100
			local := v.ValidateConfig(cfg)
			_, actual := c.OpenStream(context.Background(), cfg)
			if local == nil || actual == nil || local.Error() != actual.Error() || !speecherr.IsCode(local, speecherr.CodeUnsupported) {
				t.Fatalf("offline/live mismatch: %v / %v", local, actual)
			}
		})
	}
}

func TestOfflineTTSCapabilities(t *testing.T) {
	dialer := &websocket.Dialer{NetDialContext: func(context.Context, string, string) (net.Conn, error) {
		t.Error("offline validation opened a network connection")
		return nil, errors.New("unexpected dial")
	}}
	on := true
	for _, tc := range []struct {
		name     string
		cfg      speech.TTSConfig
		voice    string
		options  tts.ProviderOptions
		complete []tts.TextFormat
		duplex   []tts.TextFormat
	}{
		{"volcengine", speech.VolcengineTTS{APIKey: "test", Dialer: dialer}, "voice", volctts.Options{EnableSubtitle: &on}, []tts.TextFormat{tts.TextPlain, tts.TextSSML, tts.TextInlineTag}, []tts.TextFormat{tts.TextPlain, tts.TextInlineTag}},
		{"aliyun", speech.AliyunTTS{APIKey: "test", Dialer: dialer}, "voice", alitts.Options{WordTimestampEnabled: &on}, []tts.TextFormat{tts.TextPlain, tts.TextSSML}, []tts.TextFormat{tts.TextPlain}},
		{"tencent", speech.TencentTTS{AppID: "123", SecretID: "id", SecretKey: "key", Dialer: dialer}, "101001", tentts.Options{EnableSubtitle: &on}, []tts.TextFormat{tts.TextPlain}, []tts.TextFormat{tts.TextPlain}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := speech.NewTTS(tc.cfg)
			if err != nil {
				t.Fatal(err)
			}
			cap := c.(tts.CapabilityProvider).Capabilities()
			v := c.(tts.RequestValidator)
			if cap.Provider != tc.name || !cap.Batch || !cap.Streaming || !cap.Duplex || !cap.Events ||
				!slices.Equal(cap.CompleteTextFormats, tc.complete) || !slices.Equal(cap.DuplexTextFormats, tc.duplex) ||
				cap.Flush != (tc.name == "volcengine") || cap.Cancel != (tc.name == "aliyun") || cap.Reset != (tc.name == "tencent") {
				t.Fatalf("capabilities = %+v", cap)
			}
			cap.CompleteTextFormats[0] = "mutated"
			if !slices.Equal(c.(tts.CapabilityProvider).Capabilities().CompleteTextFormats, tc.complete) {
				t.Fatal("mutable capability slice reused")
			}
			for _, format := range []tts.TextFormat{tts.TextPlain, tts.TextSSML, tts.TextInlineTag} {
				req := tts.Request{Text: "hello", Voice: tc.voice, TextFormat: format}
				if err := v.ValidateRequest(req); (err == nil) != slices.Contains(tc.complete, format) {
					t.Fatalf("complete text format %s: %v", format, err)
				}
				d := tts.DuplexRequest{Voice: tc.voice, TextFormat: format}
				if err := v.ValidateDuplex(d); (err == nil) != slices.Contains(tc.duplex, format) {
					t.Fatalf("duplex text format %s: %v", format, err)
				}
			}
			req := tts.Request{Text: "hello", Voice: tc.voice, Options: tts.Options{Provider: tc.options}}
			if err := v.ValidateRequest(req); err != nil {
				t.Fatal(err)
			}
			local := v.ValidateSynthesize(req)
			_, actual := c.Synthesize(context.Background(), req)
			if local == nil || actual == nil || local.Error() != actual.Error() {
				t.Fatalf("batch metadata rejection: %v / %v", local, actual)
			}
			req.Text = ""
			local = v.ValidateRequest(req)
			_, actual = c.SynthesizeStream(context.Background(), req)
			if local == nil || actual == nil || local.Error() != actual.Error() {
				t.Fatalf("empty text mismatch: %v / %v", local, actual)
			}
		})
	}
}
