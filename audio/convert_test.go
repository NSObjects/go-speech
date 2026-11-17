package audio

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

type testTranscoder struct {
	calls int
	out   Buffer
}

func (t *testTranscoder) Convert(ctx context.Context, in Buffer, target Format) (Buffer, error) {
	t.calls++
	return t.out, nil
}

func TestConvertRouting(t *testing.T) {
	pcm := Buffer{Data: []byte{0, 0, 42, 0}, Format: Format{EncodingPCM16LE, 16000, 1}}
	ctx := context.Background()
	cloned, err := Convert(ctx, pcm, pcm.Format, nil)
	if err != nil || !bytes.Equal(cloned.Data, pcm.Data) {
		t.Fatalf("passthrough: %v", err)
	}
	cloned.Data[0] = 99
	if pcm.Data[0] == 99 {
		t.Fatal("passthrough aliases caller buffer")
	}
	resampled, err := Convert(ctx, pcm, Format{EncodingPCM16LE, 8000, 1}, nil)
	if err != nil || resampled.Format.SampleRate != 8000 {
		t.Fatalf("builtin conversion: %v", err)
	}
	compressed := Buffer{Data: []byte{'I', 'D', '3', 0, 0}, Format: Format{EncodingMP3, 16000, 1}}
	if _, err := Convert(ctx, compressed, pcm.Format, nil); !errors.Is(err, ErrUnsupportedConversion) {
		t.Fatalf("expected no decoder: %v", err)
	}
	backend := &testTranscoder{out: pcm}
	if out, err := Convert(ctx, compressed, pcm.Format, backend); err != nil || !bytes.Equal(out.Data, pcm.Data) || backend.calls != 1 {
		t.Fatalf("delegation: out=%v err=%v calls=%d", out, err, backend.calls)
	}
	backend.out.Format = Format{EncodingPCM16LE, 48000, 1}
	if _, err := Convert(ctx, compressed, pcm.Format, backend); !errors.Is(err, ErrInvalidFormat) {
		t.Fatalf("must reject wrong output format: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Convert(cancelled, pcm, pcm.Format, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
}
