package tts

import (
	"context"
	"errors"
	"io"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/internal/ttscollect"
	"github.com/NSObjects/go-speech/speecherr"
)

const (
	maxDetailedAlignments = 10000
	maxDetailedWords      = 100000
)

// Result contains the complete synthesized audio and the alignment events
// received in provider order. Alignment data may be absent when timestamps
// are disabled or the selected model does not return them.
type Result struct {
	Audio      audio.Buffer
	Alignments []Alignment
}

// ErrEventsUnsupported indicates that a stream does not implement EventReceiver.
var ErrEventsUnsupported = errors.New("tts: event receiver unsupported")

// SynthesizeDetailed submits complete text and collects audio and alignment
// events. It requires EventReceiver support and always closes the stream.
// Enable timestamps through the request's provider options when needed.
// It does not change options, retry, downgrade to audio-only synthesis or
// infer missing alignment. Audio is limited to 64 MiB; alignment and word
// counts are bounded. For long results use SynthesizeStream and RecvEvent.
// On failure no partial Result is returned.
func SynthesizeDetailed(ctx context.Context, client StreamSynthesizer, req Request) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if client == nil {
		return Result{}, detailedError(speecherr.CodeInvalidArgument, errors.New("nil synthesizer"))
	}
	stream, err := client.SynthesizeStream(ctx, req)
	if err != nil {
		return Result{}, err
	}
	if stream == nil {
		return Result{}, detailedError(speecherr.CodeProtocol, errors.New("nil synthesis stream"))
	}
	defer func() { _ = stream.Close() }()
	reader, ok := stream.(EventReceiver)
	if !ok {
		return Result{}, detailedError(speecherr.CodeUnsupported, ErrEventsUnsupported)
	}
	return collectDetailed(ctx, reader, ttscollect.MaxAudioBytes)
}

func collectDetailed(ctx context.Context, reader EventReceiver, audioLimit int) (Result, error) {
	var result Result
	var words int
	for {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		event, err := reader.RecvEvent(ctx)
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if errors.Is(err, io.EOF) {
			if len(result.Audio.Data) == 0 {
				return Result{}, detailedError(speecherr.CodeProtocol, errors.New("synthesis completed without audio"))
			}
			return result, nil
		}
		if err != nil {
			return Result{}, err
		}
		switch event.Type {
		case EventAudio:
			if event.Audio == nil || event.Alignment != nil || len(event.Audio.Data) == 0 {
				return Result{}, detailedError(speecherr.CodeProtocol, errors.New("invalid audio event"))
			}
			if len(event.Audio.Data) > audioLimit-len(result.Audio.Data) {
				return Result{}, detailedError(speecherr.CodeInvalidArgument, errors.New("batch audio exceeds limit; use SynthesizeStream"))
			}
			if len(result.Audio.Data) == 0 {
				result.Audio.Format = event.Audio.Format
			} else if event.Audio.Format != result.Audio.Format {
				return Result{}, detailedError(speecherr.CodeProtocol, errors.New("audio format changed during synthesis"))
			}
			result.Audio.Data = append(result.Audio.Data, event.Audio.Data...)
		case EventAlignment:
			if event.Alignment == nil || event.Audio != nil {
				return Result{}, detailedError(speecherr.CodeProtocol, errors.New("invalid alignment event"))
			}
			if len(result.Alignments) >= maxDetailedAlignments || len(event.Alignment.Words) > maxDetailedWords-words {
				return Result{}, detailedError(speecherr.CodeInvalidArgument, errors.New("batch alignment exceeds count limit; use SynthesizeStream"))
			}
			a := *event.Alignment
			a.Words = append([]Word(nil), a.Words...)
			result.Alignments = append(result.Alignments, a)
			words += len(a.Words)
		case EventBatchCompleted:
			// Batch completion does not imply terminal EOF or alignment completion.
		default:
			return Result{}, detailedError(speecherr.CodeProtocol, errors.New("unknown synthesis event"))
		}
	}
}

func detailedError(code speecherr.Code, err error) error {
	return &speecherr.Error{Code: code, Operation: "tts_detailed", Err: err}
}
