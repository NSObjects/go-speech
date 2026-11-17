package asr

import (
	"errors"
	"testing"
)

func TestSharedStreamSentinels(t *testing.T) {
	if !errors.Is(ErrStreamClosed, ErrStreamClosed) {
		t.Fatal("missing stream closed sentinel")
	}
	if errors.Is(ErrStreamClosed, ErrInputClosed) {
		t.Fatal("stream and audio-input closure must differ")
	}
}
