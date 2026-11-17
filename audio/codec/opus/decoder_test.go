package opus

import (
	"errors"
	"testing"

	"github.com/NSObjects/go-speech/audio"
)

func TestValidateDecoderConfiguration(t *testing.T) {
	for _, v := range []struct{ rate, channels int }{
		{0, 1}, {44100, 1}, {16000, 0}, {16000, 3}, {-100, 2},
	} {
		_, err := NewDecoder(v.rate, v.channels)
		if !errors.Is(err, audio.ErrInvalidFormat) {
			t.Errorf("%d/%d: expected invalid format, got %v", v.rate, v.channels, err)
		}
	}
}
func TestDefaultBuildHasNoNativeDependency(t *testing.T) {
	if Available() {
		t.Skip("optional native binding enabled")
	}
	d, err := NewDecoder(16000, 1)
	if d != nil || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected opt-in native dependency error, got decoder=%v err=%v", d, err)
	}
}
func TestClosedDecoder(t *testing.T) {
	if err := (*Decoder)(nil).Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := (*Decoder)(nil).Decode([]byte{1}); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}
