// Package tts defines vendor-independent text-to-speech contracts.
// It does not control device playback or conversational turn-taking.
package tts

import (
	"context"

	"github.com/NSObjects/go-speech/audio"
)

// Request specifies the text to synthesize and common output preferences.
// Voice identifiers are provider-specific; use the selected provider's voice ID.
// Providers should return the actual audio format in Buffer/Chunk, which may
// differ from the requested format if the provider does not support it.
type Request struct {
	Text   string
	Voice  string
	Format audio.Format
}

// Synthesizer performs one-shot text-to-speech synthesis.
type Synthesizer interface {
	Synthesize(ctx context.Context, req Request) (audio.Buffer, error)
}

// StreamSynthesizer generates audio incrementally from a complete text request.
// Incremental text input is a separate potential capability, not implied here.
type StreamSynthesizer interface {
	SynthesizeStream(ctx context.Context, req Request) (Stream, error)
}

// Stream yields consecutive bytes of a single synthesized audio bitstream
// until io.EOF. An individual encoded MP3/WAV/Ogg Opus chunk is not
// necessarily a standalone decodable file: concatenate the chunks or use a streaming
// decoder. Chunk.Format describes the combined output stream.
// One caller owns Recv; concurrent Recv calls are not supported. Close is
// idempotent and releases resources. The SynthesizeStream context controls
// the session lifetime.
type Stream interface {
	Recv(ctx context.Context) (audio.Chunk, error)
	Close() error
}
