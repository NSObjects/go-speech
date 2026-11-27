package asr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"slices"
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
	// PCM16 WAV is a continuous container, not a sequence of standalone WAV files.
	// Validate complete metadata and pace the original bytes without decoding on the wire.
	paced := input.Format.Encoding == audio.EncodingPCM16LE || input.Format.Encoding == audio.EncodingWAV
	if paced {
		pcmBytes := len(input.Data)
		if input.Format.Encoding == audio.EncodingWAV {
			decoded, err := audio.DecodeWAVPCM16(input.Data)
			if err != nil {
				return Result{}, fmt.Errorf("%w: %w", ErrInvalidShortAudio, err)
			}
			if decoded.Format.SampleRate != input.Format.SampleRate || decoded.Format.Channels != input.Format.Channels || len(decoded.Data) == 0 {
				return Result{}, fmt.Errorf("%w: WAV metadata or audio data mismatch", ErrInvalidShortAudio)
			}
			pcmBytes = len(decoded.Data)
		}
		frameBytes := input.Format.Channels * 2
		if pcmBytes%frameBytes != 0 {
			return Result{}, fmt.Errorf("%w: incomplete PCM frame", ErrInvalidShortAudio)
		}
		frameCount := pcmBytes / frameBytes
		if int64(frameCount) > int64(input.Format.SampleRate)*int64(maxShortAudioDuration/time.Second) {
			return Result{}, fmt.Errorf("%w: PCM exceeds 60 seconds", ErrInvalidShortAudio)
		}
		chunkSize = input.Format.SampleRate * frameBytes / 25 // 40 ms PCM frames
		if chunkSize == 0 {
			return Result{}, fmt.Errorf("%w: invalid PCM sample rate", ErrInvalidShortAudio)
		}
		chunkSize = max(chunkSize/frameBytes, 1) * frameBytes
	}

	chunks := func(yield func([]byte) bool) {
		for offset := 0; offset < len(input.Data); offset += chunkSize {
			if !yield(input.Data[offset:min(offset+chunkSize, len(input.Data))]) {
				return
			}
		}
	}
	var interval time.Duration
	if paced {
		interval = shortAudioPCMFrame
	}
	return recognizeChunks(ctx, client, cfg, chunks, interval)
}

// RecognizePacketStream collects a short recognition from complete, already
// framed audio packets. Adapters use this instead of arbitrary byte splitting
// when their protocol requires packet boundaries. Each packet represents
// interval of audio; packets are sent in order at that cadence. The adapter
// remains responsible for validating the native packet format.
func RecognizePacketStream(ctx context.Context, client StreamRecognizer, cfg Config, packets [][]byte, interval time.Duration) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := cfg.Format.Validate(); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrInvalidShortAudio, err)
	}
	if len(packets) == 0 || interval <= 0 || interval > maxShortAudioDuration || int64(len(packets)) > int64(maxShortAudioDuration/interval) {
		return Result{}, fmt.Errorf("%w: invalid packet duration or audio exceeds 60 seconds", ErrInvalidShortAudio)
	}
	total := 0
	for _, packet := range packets {
		if len(packet) == 0 || len(packet) > MaxShortAudioBytes-total {
			return Result{}, fmt.Errorf("%w: empty packet or audio exceeds byte limit", ErrInvalidShortAudio)
		}
		total += len(packet)
	}
	return recognizeChunks(ctx, client, cfg, slices.Values(packets), interval)
}

func recognizeChunks(ctx context.Context, client StreamRecognizer, cfg Config, chunks iter.Seq[[]byte], interval time.Duration) (Result, error) {
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
		if interval > 0 {
			ticker = time.NewTicker(interval)
			defer ticker.Stop()
		}
		for chunk := range chunks {
			if sendErr = stream.Send(sessionCtx, chunk); sendErr != nil {
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
	var utteranceIndex map[string]int
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
		if event.Type == EventFinal {
			if event.Text != "" {
				parts = append(parts, event.Text)
				if event.Language != "" {
					lang = event.Language
				}
			}
			if event.Utterance != nil {
				u := *event.Utterance
				if u.ID != "" {
					if utteranceIndex == nil {
						utteranceIndex = make(map[string]int)
					}
					utteranceIndex[u.ID] = len(utterances)
				}
				utterances = append(utterances, u)
			}
		} else if event.Type == EventUtteranceUpdate && event.Utterance != nil && event.Utterance.ID != "" {
			if i, ok := utteranceIndex[event.Utterance.ID]; ok && utterances[i].Text == event.Utterance.Text {
				utterances[i] = *event.Utterance
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
	result := Result{Text: strings.Join(parts, ""), Language: lang, Utterances: utterances}
	if metadata, ok := stream.(SessionMetadataReceiver); ok {
		result.Provider = metadata.SessionMetadata()
	}
	return result, nil
}
