package asr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/NSObjects/go-speech/audio"
)

// MaxShortAudioBytes limits the amount of audio accepted by Recognize.
// Long recordings should use OpenStream instead.
const MaxShortAudioBytes = 16 << 20

const shortAudioChunkBytes = 32 << 10
const shortAudioPCMFrame = 40 * time.Millisecond
const maxShortAudioDuration = 60 * time.Second

var ErrInvalidShortAudio = errors.New("asr: invalid short audio")

// RecognizeStream performs complete-audio recognition through a streaming
// adapter, while keeping Send and Recv independent to avoid socket deadlocks.
// Only confirmed final segments contribute to the returned transcript.
func RecognizeStream(ctx context.Context, client StreamRecognizer, input audio.Buffer, cfg Config) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if len(input.Data) == 0 || len(input.Data) > MaxShortAudioBytes {
		return Result{}, fmt.Errorf("%w: audio must be between 1 and %d bytes", ErrInvalidShortAudio, MaxShortAudioBytes)
	}
	if err := input.Format.Validate(); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrInvalidShortAudio, err)
	}
	if cfg.Format != (audio.Format{}) && cfg.Format != input.Format {
		return Result{}, fmt.Errorf("%w: config format differs from input format", ErrInvalidShortAudio)
	}
	cfg.Format = input.Format
	chunkSize := shortAudioChunkBytes
	if input.Format.Encoding == audio.EncodingPCM16LE {
		frameBytes := input.Format.Channels * 2
		if len(input.Data)%frameBytes != 0 {
			return Result{}, fmt.Errorf("%w: incomplete PCM frame", ErrInvalidShortAudio)
		}
		frameCount := len(input.Data) / frameBytes
		if int64(frameCount) > int64(input.Format.SampleRate)*int64(maxShortAudioDuration/time.Second) {
			return Result{}, fmt.Errorf("%w: PCM exceeds 60 seconds", ErrInvalidShortAudio)
		}
		chunkSize = input.Format.SampleRate * frameBytes / 25 // 40 ms PCM frames
		if chunkSize == 0 {
			return Result{}, fmt.Errorf("%w: invalid PCM sample rate", ErrInvalidShortAudio)
		}
		chunkSize = max(chunkSize/frameBytes, 1) * frameBytes
	}

	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := client.OpenStream(sessionCtx, cfg)
	if err != nil {
		return Result{}, err
	}
	defer stream.Close()

	sent := make(chan error, 1)
	go func() {
		var sendErr error
		var ticker *time.Ticker
		if input.Format.Encoding == audio.EncodingPCM16LE {
			ticker = time.NewTicker(shortAudioPCMFrame)
			defer ticker.Stop()
		}
		for offset := 0; offset < len(input.Data); offset += chunkSize {
			end := min(offset+chunkSize, len(input.Data))
			if sendErr = stream.Send(sessionCtx, input.Data[offset:end]); sendErr != nil {
				break
			}
			if ticker != nil {
				select {
				case <-ticker.C:
				case <-sessionCtx.Done():
					sendErr = sessionCtx.Err()
				}
				if sendErr != nil {
					break
				}
			}
		}
		if sendErr == nil {
			sendErr = stream.CloseSend(sessionCtx)
		}
		if sendErr != nil {
			_ = stream.Close()
		}
		sent <- sendErr
	}()

	var parts []string
	var lang string
	var utterances []Utterance
	var recvErr error
	for {
		event, err := stream.Recv(sessionCtx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			recvErr = err
			break
		}
		if event.Type == EventFinal && event.Text != "" {
			parts = append(parts, event.Text)
			if event.Utterance != nil {
				utterances = append(utterances, *event.Utterance)
			}
			if event.Language != "" {
				lang = event.Language
			}
		}
	}
	// Cancel/close on receive failures so an in-flight Send exits promptly.
	if recvErr != nil {
		_ = stream.Close()
	}
	sendErr := <-sent
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	// Preserve the real receive failure when closing the socket causes
	// the sender to observe only ErrStreamClosed.
	if recvErr != nil && !errors.Is(recvErr, ErrStreamClosed) {
		if sendErr == nil || errors.Is(sendErr, ErrStreamClosed) || errors.Is(sendErr, context.Canceled) {
			return Result{}, recvErr
		}
	}
	if sendErr != nil {
		return Result{}, sendErr
	}
	if recvErr != nil {
		return Result{}, recvErr
	}
	return Result{Text: strings.Join(parts, ""), Language: lang, Utterances: utterances}, nil
}
