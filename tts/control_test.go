package tts

import (
	"context"
	"errors"
	"testing"

	"github.com/NSObjects/go-speech/speecherr"
)

type testPrewarmer struct {
	ctx   context.Context
	voice string
	count int
	calls int
	err   error
}

func (w *testPrewarmer) Warmup(ctx context.Context, voice string, count int) error {
	w.ctx, w.voice, w.count = ctx, voice, count
	w.calls++
	return w.err
}

func TestWarmupDelegation(t *testing.T) {
	ctx := context.Background()
	w := &testPrewarmer{}
	if err := Warmup(ctx, w, "voice", 2); err != nil {
		t.Fatal(err)
	}
	if w.ctx != ctx || w.voice != "voice" || w.count != 2 || w.calls != 1 {
		t.Fatal("warmup arguments were not passed through")
	}
	w.err = errors.New("provider failure")
	if err := Warmup(ctx, w, "voice", 1); err != w.err || w.calls != 2 {
		t.Fatalf("provider error changed or retried: %v, calls=%d", err, w.calls)
	}
	if err := Warmup(ctx, struct{}{}, "voice", 1); !speecherr.IsCode(err, speecherr.CodeUnsupported) {
		t.Fatalf("unsupported client: %v", err)
	}
	if err := Warmup(ctx, nil, "voice", 1); !speecherr.IsCode(err, speecherr.CodeInvalidArgument) {
		t.Fatalf("nil client: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := Warmup(canceled, w, "voice", 1); err != context.Canceled || w.calls != 2 {
		t.Fatalf("canceled call reached provider: %v", err)
	}
}
