// Package ttscollect collects a bounded audio bitstream from a TTS stream.
package ttscollect

import (
	"context"
	"errors"
	"io"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
)

// MaxAudioBytes is the common one-shot synthesis memory limit.
const MaxAudioBytes = 64 << 20

type reader interface {
	Recv(context.Context) (audio.Chunk, error)
}

// Collect consumes one audio bitstream. The caller owns and closes the stream.
// It returns no partial audio on failure and never transcodes or retries.
func Collect(ctx context.Context, r reader, provider string) (audio.Buffer, error) {
	return collect(ctx, r, provider, MaxAudioBytes)
}

func collect(ctx context.Context, r reader, provider string, limit int) (audio.Buffer, error) {
	var out audio.Buffer
	for {
		if err := ctx.Err(); err != nil {
			return audio.Buffer{}, err
		}
		chunk, err := r.Recv(ctx)
		if errors.Is(err, io.EOF) {
			if len(out.Data) == 0 {
				return audio.Buffer{}, failure(provider, speecherr.CodeProtocol, "synthesis completed without audio")
			}
			return out, nil
		}
		if err != nil {
			return audio.Buffer{}, err
		}
		if err := ctx.Err(); err != nil {
			return audio.Buffer{}, err
		}
		if len(chunk.Data) == 0 {
			return audio.Buffer{}, failure(provider, speecherr.CodeProtocol, "empty audio chunk")
		}
		if len(chunk.Data) > limit-len(out.Data) {
			return audio.Buffer{}, failure(provider, speecherr.CodeInvalidArgument, "batch audio exceeds limit; use SynthesizeStream")
		}
		if len(out.Data) == 0 {
			out.Format = chunk.Format
		} else if chunk.Format != out.Format {
			return audio.Buffer{}, failure(provider, speecherr.CodeProtocol, "audio format changed during synthesis")
		}
		out.Data = append(out.Data, chunk.Data...)
	}
}

func failure(provider string, code speecherr.Code, message string) error {
	return &speecherr.Error{Provider: provider, Operation: "tts", Code: code, Err: errors.New(message)}
}
