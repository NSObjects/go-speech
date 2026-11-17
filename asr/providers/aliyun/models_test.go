package aliyun

import (
	"testing"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
)

func TestModelParameters(t *testing.T) {
	cases := []struct {
		name    string
		model   string
		rate    int
		options Config
		hints   []string
		wantKey string
		want    any
		code    speecherr.Code
	}{
		{name: "qwen streaming", model: ModelQwenAudio31Streaming, rate: 16000, options: Config{KeepDialect: true, VADModel: "far_field_meeting_16k"}, wantKey: "keep_dialect", want: true},
		{name: "qwen message", model: ModelQwenAudio31Message, rate: 16000, options: Config{IntermediateResults: true, DisfluencyRemoval: true}, wantKey: "intermediate_result_enabled", want: true},
		{name: "fun", model: ModelFunASRRealtime, rate: 16000, options: Config{Heartbeat: true, MaxSentenceSilenceMs: 700}, wantKey: "max_sentence_silence", want: 700},
		{name: "paraformer", model: ModelParaformerRealtime, rate: 16000, options: Config{DisfluencyRemoval: true}, wantKey: "disfluency_removal_enabled", want: true},
		{name: "paraformer 8k", model: ModelParaformerRealtime8K, rate: 8000, options: Config{Heartbeat: true}, wantKey: "heartbeat", want: true},
		{name: "message rejects 8k", model: ModelQwenAudio31Message, rate: 8000, code: speecherr.CodeUnsupported},
		{name: "paraformer 8k rejects 16k", model: ModelParaformerRealtime8K, rate: 16000, code: speecherr.CodeUnsupported},
		{name: "fun rejects instant hints", model: ModelFunASRRealtime, rate: 16000, hints: []string{"hello"}, code: speecherr.CodeUnsupported},
		{name: "fun rejects dialect", model: ModelFunASRRealtime, rate: 16000, options: Config{KeepDialect: true}, code: speecherr.CodeUnsupported},
		{name: "invalid vocabulary", model: ModelQwenAudio31Streaming, rate: 16000, options: Config{Vocabulary: map[string]int{"hello": 70}}, code: speecherr.CodeInvalidArgument},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.options.Model = tc.model
			params, err := tc.options.parameters(asr.Config{Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: tc.rate, Channels: 1}, Hints: tc.hints}, "pcm")
			if tc.code != "" {
				if !speecherr.IsCode(err, tc.code) {
					t.Fatalf("want %s, got %v", tc.code, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if params[tc.wantKey] != tc.want {
				t.Fatalf("%s=%v, want %v", tc.wantKey, params[tc.wantKey], tc.want)
			}
		})
	}
}
