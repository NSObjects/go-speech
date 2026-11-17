// Package audio contains transport-independent audio data types shared by ASR and TTS.
package audio

import (
	"errors"
	"fmt"
)

// Encoding identifies the representation of audio bytes.
// Providers may support only a subset of the declared encodings.
type Encoding string

const (
	EncodingPCM16LE Encoding = "pcm_s16le"
	EncodingWAV     Encoding = "wav"
	EncodingMP3     Encoding = "mp3"
	EncodingOpus    Encoding = "opus"
	EncodingAAC     Encoding = "aac"
	EncodingAMR     Encoding = "amr"
)

// ErrInvalidFormat indicates an invalid audio format description.
var ErrInvalidFormat = errors.New("audio: invalid format")

// Format describes audio bytes. SampleRate is in Hz; Channels is the channel count.
// A valid Format does not imply that a given provider supports it.
type Format struct {
	Encoding   Encoding
	SampleRate int
	Channels   int
}

// Validate checks the structural validity of the format.
func (f Format) Validate() error {
	if f.Encoding == "" {
		return fmt.Errorf("%w: encoding is required", ErrInvalidFormat)
	}
	if f.SampleRate <= 0 {
		return fmt.Errorf("%w: sample rate must be positive", ErrInvalidFormat)
	}
	if f.Channels <= 0 {
		return fmt.Errorf("%w: channels must be positive", ErrInvalidFormat)
	}
	return nil
}

// Buffer is a complete audio payload. Data is caller-owned; implementations
// must document whether they retain it beyond the method call.
type Buffer struct {
	Data   []byte
	Format Format
}

// Chunk is a piece of output audio from a streaming TTS provider.
// Chunk boundaries are transport boundaries, not necessarily audio frames.
type Chunk struct {
	Data   []byte
	Format Format
}
