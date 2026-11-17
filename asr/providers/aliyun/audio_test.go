package aliyun

import (
	"errors"
	"testing"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
)

func TestSupportedAudioModels(t *testing.T) {
	models := []struct {
		model string
		rate  int
	}{
		{ModelQwenAudio31Streaming, 48000},
		{ModelQwenAudio31Message, 16000},
		{ModelFunASRRealtime, 48000},
		{ModelParaformerRealtime, 48000},
		{ModelParaformerRealtime8K, 8000},
	}
	encodings := []audio.Encoding{
		audio.EncodingPCM16LE, audio.EncodingWAV, audio.EncodingMP3,
		audio.EncodingOpus, audio.EncodingSpeex, audio.EncodingAAC, audio.EncodingAMR,
	}
	for _, model := range models {
		t.Run(model.model, func(t *testing.T) {
			c, err := SupportedAudio(model.model)
			if err != nil {
				t.Fatal(err)
			}
			for _, encoding := range encodings {
				input := audio.Format{Encoding: encoding, SampleRate: model.rate, Channels: 1}
				if err = c.Validate(input); err != nil {
					t.Errorf("%s rejected: %v", encoding, err)
				}
				name, err := mapAudioFormat(input)
				if err != nil {
					t.Errorf("mapping %s: %v", encoding, err)
				}
				if encoding == audio.EncodingSpeex && name != "speex" {
					t.Errorf("Speex mapped to %q", name)
				}
			}
			other := audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: model.rate, Channels: 2}
			if c.Supports(other) {
				t.Fatal("unexpected stereo acceptance")
			}
		})
	}
	for _, tc := range []struct {
		model string
		hz    int
	}{
		{ModelQwenAudio31Message, 48000},
		{ModelParaformerRealtime8K, 16000},
	} {
		c, _ := SupportedAudio(tc.model)
		if err := c.Validate(audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: tc.hz, Channels: 1}); !errors.Is(err, audio.ErrUnsupportedFormat) {
			t.Fatalf("model %s must reject %d Hz: %v", tc.model, tc.hz, err)
		}
		cfg := Config{Model: tc.model}
		_, err := cfg.parameters(asr.Config{Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: tc.hz, Channels: 1}}, "pcm")
		if !speecherr.IsCode(err, speecherr.CodeUnsupported) {
			t.Fatalf("wrong error: %v", err)
		}
	}
	if _, err := SupportedAudio("not-a-model"); !speecherr.IsCode(err, speecherr.CodeUnsupported) {
		t.Fatal("unknown model accepted")
	}
}
