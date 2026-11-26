// Package tts defines vendor-independent text-to-speech contracts.
// It does not control device playback or conversational turn-taking.
package tts

import (
	"context"
	"errors"

	"github.com/NSObjects/go-speech/audio"
)

// ErrStreamClosed indicates that a TTS stream was explicitly closed before
// completion. It is not io.EOF, which is reserved for successful synthesis.
// Check with errors.Is(err, tts.ErrStreamClosed) regardless of the provider.
var ErrStreamClosed = errors.New("tts: stream closed")

// ErrInputClosed indicates text input has already ended.
var ErrInputClosed = errors.New("tts: text input closed")

// ErrDuplexUnsupported indicates a provider has no incremental-text implementation.
var ErrDuplexUnsupported = errors.New("tts: duplex synthesis unsupported")

// Request describes one complete text-to-speech synthesis.
//
// Voice is always a provider-specific voice identifier: it is not translated
// between vendors. Format requests the output encoding, sample rate, and
// channels. A zero Format selects the chosen provider's default (which may
// differ across providers). An explicit unsupported format is rejected; no
// adapter silently resamples or transcodes audio.
type Request struct {
	Text       string
	Voice      string
	Format     audio.Format
	TextFormat TextFormat
}

// Synthesizer performs one-shot text-to-speech synthesis, returning the full
// audio bitstream in memory. For long output, use StreamSynthesizer.
type Synthesizer interface {
	Synthesize(ctx context.Context, req Request) (audio.Buffer, error)
}

// StreamSynthesizer generates audio incrementally from a complete text
// request. It does not support incremental text input.
type StreamSynthesizer interface {
	SynthesizeStream(ctx context.Context, req Request) (Stream, error)
}

// Client implements both one-shot and streaming synthesis.
// The three built-in providers implement this interface. Third-party
// providers may implement just Synthesizer or StreamSynthesizer instead.
type Client interface {
	Synthesizer
	StreamSynthesizer
}

// Stream yields consecutive slices of one synthesized audio bitstream.
//
// A single MP3, WAV, or Ogg Opus Chunk.Data is not necessarily a standalone
// decodable file. Concatenate chunks in receive order, or feed them to a
// streaming decoder. Chunk.Format identifies the audio encoding/rate/channels.
// A stream supports one Recv caller; concurrent Recv calls are not supported.
//
// Recv returns io.EOF repeatedly after successful completion. After a
// terminal error, subsequent Recv calls retain that error. Explicit Close
// before completion returns ErrStreamClosed from Recv, while canceling the
// SynthesizeStream or Recv context preserves the corresponding context error.
// Close is idempotent, releases network resources, and is always required
// even after EOF or failure.
type Stream interface {
	Recv(ctx context.Context) (audio.Chunk, error)
	Close() error
}
