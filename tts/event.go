package tts

import (
	"context"
	"errors"
	"time"

	"github.com/NSObjects/go-speech/audio"
)

// ErrReceiveMode indicates that the session has already selected the other
// receive API. It does not close the stream or consume a provider event.
var ErrReceiveMode = errors.New("tts: cannot mix Recv and RecvEvent")

type EventType string

const (
	EventAudio          EventType = "audio"
	EventAlignment      EventType = "alignment"
	EventBatchCompleted EventType = "batch_completed"
)

// EventReceiver is an optional interface implemented by event-capable streams.
// Select RecvEvent or Stream.Recv for the entire session, not both. Use one
// receiver. Events preserve wire order; EOF and terminal errors follow Stream's
// contract. BatchCompleted marks an audio-batch boundary, not necessarily
// the end of its alignment delivery. Some providers return independently
// encoded audio bitstreams for successive batches.
type EventReceiver interface {
	RecvEvent(ctx context.Context) (Event, error)
}

// Event contains one audio, alignment or batch completion notification.
// Audio and Alignment are non-nil only for their respective event types.
type Event struct {
	Type      EventType
	Audio     *audio.Chunk
	Alignment *Alignment
}

// Alignment reports provider-supplied timing. Timing offsets are relative to
// the provider's audio timeline, not wall-clock time or device playback.
type Alignment struct {
	Text  string
	Words []Word
	// Provider holds optional provider-native alignment details.
	Provider any
}

// Word is a provider-reported timed text unit (a word or character).
type Word struct {
	Text      string
	StartTime time.Duration
	EndTime   time.Duration
	// Phoneme is a provider-reported phonetic representation, when available.
	Phoneme string
}
