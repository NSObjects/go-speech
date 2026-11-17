package audio

import (
	"errors"
	"testing"
)

func TestFormatValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input Format
		valid bool
	}{
		{"pcm", Format{EncodingPCM16LE, 16000, 1}, true},
		{"missing encoding", Format{SampleRate: 16000, Channels: 1}, false},
		{"missing rate", Format{Encoding: EncodingWAV, Channels: 1}, false},
		{"negative rate", Format{EncodingMP3, -1, 1}, false},
		{"missing channels", Format{EncodingOpus, 48000, 0}, false},
		{"extension encoding", Format{"vendor_custom", 16000, 1}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.input.Validate()
			if tc.valid && err != nil {
				t.Fatalf("expected valid format, got %v", err)
			}
			if !tc.valid && !errors.Is(err, ErrInvalidFormat) {
				t.Fatalf("expected ErrInvalidFormat, got %v", err)
			}
		})
	}
}
