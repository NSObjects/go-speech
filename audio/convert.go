package audio

import (
	"context"
)

// Transcoder is an optional implementation for codecs not provided by the
// Go standard library. Implementations may invoke external binaries or link
// native libraries. Implementations must document their latency and resource
// limits, and must honor cancellation.
type Transcoder interface {
	Convert(ctx context.Context, input Buffer, target Format) (Buffer, error)
}

// Convert converts a *complete* audio buffer to the target format.
//
// PCM16LE and PCM16 WAV conversions use the built-in implementation. For
// compressed input, a matching format is copied without transcoding; otherwise
// Convert delegates to backend. It does not modify input and never silently
// changes the format to something different from target. This is not a
// streaming API; use a stateful converter for realtime microphone audio.
func Convert(ctx context.Context, input Buffer, target Format, backend Transcoder) (Buffer, error) {
	if err := ctx.Err(); err != nil {
		return Buffer{}, err
	}
	if err := target.Validate(); err != nil {
		return Buffer{}, err
	}
	if err := ValidateBuffer(input); err != nil {
		return Buffer{}, err
	}
	if input.Format == target {
		return Buffer{Data: append([]byte(nil), input.Data...), Format: target}, nil
	}
	if input.Format.Encoding == EncodingPCM16LE || input.Format.Encoding == EncodingWAV {
		if target.Encoding == EncodingPCM16LE || target.Encoding == EncodingWAV {
			return ConvertPCM16(input, target)
		}
	}
	if backend == nil {
		return Buffer{}, ErrUnsupportedConversion
	}
	result, err := backend.Convert(ctx, input, target)
	if err != nil {
		return Buffer{}, err
	}
	if result.Format != target {
		return Buffer{}, ErrInvalidFormat
	}
	if err := ValidateBuffer(result); err != nil {
		return Buffer{}, err
	}
	return result, nil
}
