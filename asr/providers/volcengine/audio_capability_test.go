package volcengine

import (
	"errors"
	"testing"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
)

func TestSupportedAudioMatchesOptimizedEndpoint(t *testing.T) {
	capability := SupportedAudio()
	supported := []struct {
		format audio.Format
		name   string
		codec  string
	}{
		{audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1}, "pcm", "raw"},
		{audio.Format{Encoding: audio.EncodingWAV, SampleRate: 16000, Channels: 2}, "wav", "raw"},
		{audio.Format{Encoding: audio.EncodingMP3, SampleRate: 16000, Channels: 1}, "mp3", "raw"},
		{audio.Format{Encoding: audio.EncodingOpus, SampleRate: 16000, Channels: 1}, "ogg", "opus"},
	}
	for _, tc := range supported {
		if err := capability.Validate(tc.format); err != nil {
			t.Fatalf("valid format %+v: %v", tc.format, err)
		}
		format, codec, err := modelAudio(tc.format)
		if err != nil || format != tc.name || codec != tc.codec {
			t.Fatalf("format %+v mapped to %q/%q, err=%v; want %q/%q",
				tc.format, format, codec, err, tc.name, tc.codec)
		}
	}
}

func TestVolcengineAudioErrorContract(t *testing.T) {
	for _, tc := range []struct {
		name       string
		format     audio.Format
		code       speecherr.Code
		sentinel   error
	}{
		{"missing encoding", audio.Format{SampleRate: 16000, Channels: 1}, speecherr.CodeInvalidArgument, audio.ErrInvalidFormat},
		{"zero sample rate", audio.Format{Encoding: audio.EncodingPCM16LE, Channels: 1}, speecherr.CodeInvalidArgument, audio.ErrInvalidFormat},
		{"zero channels", audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000}, speecherr.CodeInvalidArgument, audio.ErrInvalidFormat},
		{"unsupported encoding", audio.Format{Encoding: audio.EncodingAAC, SampleRate: 16000, Channels: 1}, speecherr.CodeUnsupported, audio.ErrUnsupportedFormat},
		{"unsupported sample rate", audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 8000, Channels: 1}, speecherr.CodeUnsupported, audio.ErrUnsupportedFormat},
		{"unsupported channels", audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 3}, speecherr.CodeUnsupported, audio.ErrUnsupportedFormat},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := modelAudio(tc.format)
			if !speecherr.IsCode(err, tc.code) || !errors.Is(err, tc.sentinel) {
				t.Fatalf("format %+v: got %v, want %s and errors.Is(%v)",
					tc.format, err, tc.code, tc.sentinel)
			}
			if tc.code == speecherr.CodeUnsupported {
				if err := SupportedAudio().Validate(tc.format); !errors.Is(err, audio.ErrUnsupportedFormat) {
					t.Fatalf("capability disagrees with provider for %+v: %v", tc.format, err)
				}
			}
		})
	}
}
