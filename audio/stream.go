package audio

import "context"

// DecoderStream incrementally decodes a single continuous compressed audio
// stream to raw PCM. Write receives successive bytes from the *same* encoded
// stream (including its initial header, if applicable); Read consumes decoded
// bytes. Write and Read must be driven concurrently for long streams so that
// backpressure cannot deadlock the producer.
//
// A decoder supports at most one concurrent writer and one concurrent reader;
// these two operations may overlap. Output chunks are byte boundaries, not
// guaranteed to be aligned to complete PCM sample frames. Use a stateful PCM
// consumer if a Read splits a sample.
//
// CloseInput signals end-of-stream and allows the decoder to flush its tail.
// After draining Read to io.EOF, the caller must still Close to free resources.
// Canceling a Read/Write context may terminate the entire decoder session.
type DecoderStream interface {
	Write(ctx context.Context, encoded []byte) (int, error)
	Read(ctx context.Context, pcm []byte) (int, error)
	CloseInput() error
	OutputFormat() Format
	Close() error
}
