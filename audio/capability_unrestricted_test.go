package audio

import (
	"errors"
	"testing"
)

func TestCapabilityUnrestrictedRatesAndChannels(t *testing.T) {
	t.Parallel()
	capability := Capability{Encodings: []Encoding{EncodingPCM16LE}}
	for _, f := range []Format{
		{Encoding: EncodingPCM16LE, SampleRate: 8000, Channels: 1},
		{Encoding: EncodingPCM16LE, SampleRate: 48000, Channels: 2},
	} {
		if err := capability.Validate(f); err != nil {
			t.Fatalf("unrestricted rate/channel rejected %+v: %v", f, err)
		}
	}
	if err := capability.Validate(Format{Encoding: EncodingMP3, SampleRate: 16000, Channels: 1}); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("unlisted encoding should be unsupported, got %v", err)
	}
	if (Capability{}).Supports(Format{Encoding: EncodingPCM16LE, SampleRate: 16000, Channels: 1}) {
		t.Fatal("empty encoding list must not accept a format")
	}
}
