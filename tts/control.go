package tts

import (
	"context"
	"errors"

	"github.com/NSObjects/go-speech/speecherr"
)

// ErrSynthesisCanceled indicates a provider-confirmed cancellation, not
// successful synthesis (io.EOF) or a locally closed transport.
var ErrSynthesisCanceled = errors.New("tts: synthesis canceled")

// Flusher submits buffered input without closing the text input. A nil
// error means the command was sent, not that synthesis has completed.
// The provider may report completion through EventBatchCompleted.
// Encoded output may have an independent header per batch. See the provider
// contract; use RecvEvent to preserve batch boundaries when needed.
type Flusher interface {
	Flush(ctx context.Context) error
}

// Resetter clears text buffered by the provider that has not started
// synthesis. Already-generated audio and in-progress synthesis are not
// affected. A nil error means the command was sent.
type Resetter interface {
	Reset(ctx context.Context) error
}

// Canceler requests native cancellation before CloseSend. A nil error
// means the command was sent. Continue receiving until ErrSynthesisCanceled
// confirms completion, or a transport/provider error occurs. Already queued
// audio may still be received. The canceled task cannot accept more input.
// Close releases resources even if the cancellation is not acknowledged.
type Canceler interface {
	Cancel(ctx context.Context) error
}

// Prewarmer optionally prepares idle WebSocket connections for future TTS
// requests. Warmup is synchronous and best effort; count is a target idle
// count, not an additional dial count. A nil result does not guarantee that
// capacity is available when a later request arrives. Requires pooling.
// Voice is required for providers with voice-specific endpoints (Seed-TTS);
// providers with voice-independent endpoints (CosyVoice) ignore it.
// No synthesis task is started and no background replenishment is scheduled.
type Prewarmer interface {
	Warmup(ctx context.Context, voice string, count int) error
}

// Warmup prepares idle connections on a client that implements Prewarmer.
// Both complete-text and duplex client interfaces can be passed directly.
// The provider validates its pool configuration, voice and count. A client
// without Prewarmer support returns CodeUnsupported; errors are not ignored.
// No capability query, synthesis request, retry or fallback is performed.
func Warmup(ctx context.Context, client any, voice string, count int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if client == nil {
		return &speecherr.Error{Code: speecherr.CodeInvalidArgument, Operation: "tts_warmup", Err: errors.New("nil synthesizer")}
	}
	warmer, ok := client.(Prewarmer)
	if !ok {
		return &speecherr.Error{Code: speecherr.CodeUnsupported, Operation: "tts_warmup", Err: errors.New("connection warmup unsupported")}
	}
	return warmer.Warmup(ctx, voice, count)
}
