package aliyun

import (
	"errors"
	"testing"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
)

func TestAliyunAudioErrorContract(t *testing.T) {
	for _, tc := range []struct {
		name     string
		format   audio.Format
		code     speecherr.Code
		sentinel error
	}{
		{"missing encoding", audio.Format{SampleRate: 16000, Channels: 1}, speecherr.CodeInvalidArgument, audio.ErrInvalidFormat},
		{"zero rate", audio.Format{Encoding: audio.EncodingPCM16LE, Channels: 1}, speecherr.CodeInvalidArgument, audio.ErrInvalidFormat},
		{"zero channels", audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000}, speecherr.CodeInvalidArgument, audio.ErrInvalidFormat},
		{"unsupported channels", audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 2}, speecherr.CodeUnsupported, audio.ErrUnsupportedFormat},
		{"unsupported encoding", audio.Format{Encoding: audio.Encoding("unknown"), SampleRate: 16000, Channels: 1}, speecherr.CodeUnsupported, audio.ErrUnsupportedFormat},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := mapAudioFormat(tc.format)
			if !speecherr.IsCode(err, tc.code) || !errors.Is(err, tc.sentinel) {
				t.Fatalf("format %+v: got %v, want code %s and sentinel %v",
					tc.format, err, tc.code, tc.sentinel)
			}
		})
	}
}

func TestAliyunModelSpecificFormatErrorPreservesCause(t *testing.T) {
	cfg := Config{Model: ModelQwenAudio31Message}
	format := audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 8000, Channels: 1}
	_, err := cfg.parameters(asr.Config{Format: format}, "pcm")
	if !speecherr.IsCode(err, speecherr.CodeUnsupported) ||
		!errors.Is(err, audio.ErrUnsupportedFormat) {
		t.Fatalf("model rejected format without preserving audio cause: %v", err)
	}
	if cap, err := SupportedAudio(cfg.Model); err != nil || !errors.Is(cap.Validate(format), audio.ErrUnsupportedFormat) {
		t.Fatalf("model capability disagrees with provider: capability=%+v err=%v", cap, err)
	}
}
