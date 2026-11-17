// Package ffmpeg implements an optional, bounded *complete-buffer* audio
// transcoder and a persistent compressed audio stream decoder using an installed
// FFmpeg executable. No FFmpeg process is started merely by importing this
// package. The Converter performs one subprocess per complete audio buffer;
// OpenStreamDecoder persists one subprocess across many audio chunks.
package ffmpeg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/NSObjects/go-speech/audio"
)

const (
	defaultMaxInput  = 32 << 20
	defaultMaxOutput = 64 << 20
)

var (
	// ErrInputTooLarge indicates the input exceeds the configured size cap.
	ErrInputTooLarge = errors.New("ffmpeg: input too large")
	// ErrOutputTooLarge indicates that decoded audio exceeds the size cap.
	ErrOutputTooLarge = errors.New("ffmpeg: output too large")
)

// Converter is an opt-in FFmpeg backend. Binary defaults to "ffmpeg" from
// PATH. MaxInputBytes and MaxOutputBytes default to 32MiB and 64MiB.
// Negative limits are invalid. Conversion supports only PCM16LE or PCM16 WAV
// *output*, while input may be compressed or PCM/WAV. Limits and context
// cancellation protect subprocess resource use; callers must separately cap
// untrusted audio duration and concurrent conversions.
type Converter struct {
	Binary         string
	MaxInputBytes  int64
	MaxOutputBytes int64
}

var _ audio.Transcoder = Converter{}

// Convert decodes and (if needed) resamples a complete buffer. The resulting
// audio has exactly target.SampleRate and target.Channels, and PCM16LE or WAV
// encoding. Input is always delivered over stdin, never a remote URL. Audio
// is copied; the process does not inherit application credentials.
func (c Converter) Convert(ctx context.Context, input audio.Buffer, target audio.Format) (audio.Buffer, error) {
	if err := ctx.Err(); err != nil {
		return audio.Buffer{}, err
	}
	if err := target.Validate(); err != nil {
		return audio.Buffer{}, err
	}
	if target.Encoding != audio.EncodingPCM16LE && target.Encoding != audio.EncodingWAV {
		return audio.Buffer{}, fmt.Errorf("%w: FFmpeg backend only outputs PCM16LE/WAV", audio.ErrUnsupportedConversion)
	}
	if err := audio.ValidateBuffer(input); err != nil {
		return audio.Buffer{}, err
	}
	maxInput, maxOutput := c.MaxInputBytes, c.MaxOutputBytes
	if maxInput < 0 || maxOutput < 0 {
		return audio.Buffer{}, fmt.Errorf("%w: limits must be nonnegative", audio.ErrInvalidFormat)
	}
	if maxInput == 0 {
		maxInput = defaultMaxInput
	}
	if maxOutput == 0 {
		maxOutput = defaultMaxOutput
	}
	if int64(len(input.Data)) > maxInput {
		return audio.Buffer{}, ErrInputTooLarge
	}
	formatName, err := inputName(input.Format.Encoding)
	if err != nil {
		return audio.Buffer{}, err
	}
	binaryName := c.Binary
	if binaryName == "" {
		binaryName = "ffmpeg"
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin",
		"-f", formatName}
	if input.Format.Encoding == audio.EncodingPCM16LE {
		args = append(args, "-ar", fmt.Sprint(input.Format.SampleRate),
			"-ac", fmt.Sprint(input.Format.Channels))
	}
	args = append(args, "-i", "pipe:0", "-map", "0:a:0", "-vn", "-sn", "-dn",
		"-ac", fmt.Sprint(target.Channels), "-ar", fmt.Sprint(target.SampleRate),
		"-c:a", "pcm_s16le", "-f", "s16le", "pipe:1")

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(runCtx, binaryName, args...)
	cmd.Stdin = bytes.NewReader(input.Data)
	output := &boundedOutput{limit: maxOutput, cancel: cancel}
	cmd.Stdout = output
	var stderr limitedStderr
	cmd.Stderr = &stderr

	err = cmd.Run()
	if output.exceeded {
		return audio.Buffer{}, ErrOutputTooLarge
	}
	if ctx.Err() != nil {
		return audio.Buffer{}, ctx.Err()
	}
	if err != nil {
		return audio.Buffer{}, fmt.Errorf("ffmpeg: decoding %s: %w: %s",
			input.Format.Encoding, err, stderr.String())
	}
	if output.buf.Len() == 0 {
		return audio.Buffer{}, fmt.Errorf("%w: FFmpeg returned no samples", audio.ErrInvalidData)
	}
	pcmFormat := audio.Format{Encoding: audio.EncodingPCM16LE,
		SampleRate: target.SampleRate, Channels: target.Channels}
	raw := audio.Buffer{Data: output.buf.Bytes(), Format: pcmFormat}
	if err := audio.ValidateBuffer(raw); err != nil {
		return audio.Buffer{}, err
	}
	if target.Encoding == audio.EncodingWAV {
		return audio.EncodeWAVPCM16(raw)
	}
	return raw, nil
}

func inputName(enc audio.Encoding) (string, error) {
	switch enc {
	case audio.EncodingPCM16LE:
		return "s16le", nil
	case audio.EncodingWAV:
		return "wav", nil
	case audio.EncodingMP3:
		return "mp3", nil
	case audio.EncodingOpus, audio.EncodingSpeex:
		return "ogg", nil
	case audio.EncodingAAC:
		return "aac", nil
	case audio.EncodingAMR:
		return "amr", nil
	default:
		return "", fmt.Errorf("%w: unknown input codec %q", audio.ErrUnsupportedConversion, enc)
	}
}

// boundedOutput stops the process if audio output would exceed its cap.
type boundedOutput struct {
	buf      bytes.Buffer
	limit    int64
	cancel   context.CancelFunc
	exceeded bool
}

func (w *boundedOutput) Write(p []byte) (int, error) {
	remaining := w.limit - int64(w.buf.Len())
	if int64(len(p)) > remaining {
		w.exceeded = true
		w.cancel()
		return 0, ErrOutputTooLarge
	}
	return w.buf.Write(p)
}

// limitedStderr discards content after 4 KiB and prevents unbounded logging.
type limitedStderr struct{ b bytes.Buffer }

func (w *limitedStderr) Write(p []byte) (int, error) {
	size := len(p)
	if remaining := 4096 - w.b.Len(); remaining > 0 {
		if size > remaining {
			_, _ = w.b.Write(p[:remaining])
		} else {
			_, _ = w.b.Write(p)
		}
	}
	return size, nil
}

func (w *limitedStderr) String() string {
	return strings.TrimSpace(w.b.String())
}

var _ io.Writer = (*boundedOutput)(nil)
