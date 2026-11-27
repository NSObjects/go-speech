// Package asr defines vendor-independent automatic speech recognition contracts.
// It does not manage VAD, device playback, or conversational turn-taking.
package asr

import (
	"context"

	"github.com/NSObjects/go-speech/audio"
)

// Config specifies recognition parameters for a single request.
type Config struct {
	Format   audio.Format
	Language string // Optional BCP 47 language tag; empty lets the provider decide.
	Hints    []string
	Options  Options
}

// Result contains the final text from a non-streaming recognition request.
type Result struct {
	Text     string
	Language string
	// Utterances contains available final-segment metadata in receive order.
	Utterances []Utterance
	// Provider contains optional provider-native session metadata.
	Provider any
}

// Recognizer provides one-shot recognition of an entire audio buffer.
type Recognizer interface {
	Recognize(ctx context.Context, input audio.Buffer, cfg Config) (Result, error)
}

// Client supports both whole-buffer and live streaming recognition.
type Client interface {
	Recognizer
	StreamRecognizer
}

// StreamRecognizer opens streaming ASR sessions.
type StreamRecognizer interface {
	OpenStream(ctx context.Context, cfg Config) (Stream, error)
}

// EventType identifies a speech recognition event.
type EventType string

const (
	// EventPartial is a hypothesis that may change as more audio arrives.
	EventPartial EventType = "partial"
	// EventFinal is a confirmed transcript segment, not necessarily the end of a turn.
	EventFinal EventType = "final"
	// EventSpeechStart and EventSpeechEnd are optional provider endpoint events.
	EventSpeechStart EventType = "speech_start"
	EventSpeechEnd   EventType = "speech_end"
)

// Event is emitted from Stream. Providers may omit language, speaker IDs,
// and endpoint events.
type Event struct {
	Type     EventType
	Text     string
	Language string
	// SpeakerID is optional provider-reported, session-local speaker metadata.
	// nil means the provider did not supply an ID; -1 means unknown/pending;
	// 0 and positive values are valid speaker labels, not durable identities.
	// Tencent's speaker-enabled V2 engine emits values -1 or 0..9.
	SpeakerID *int
	// Utterance is optional detailed metadata for this transcript segment.
	Utterance *Utterance
}

// SessionMetadataReceiver optionally exposes provider-native session metadata.
// Read after draining Recv to EOF to obtain the final snapshot.
type SessionMetadataReceiver interface {
	SessionMetadata() any
}

// Stream is a bidirectional recognition session. A caller may Send and Recv
// concurrently. Recv returns io.EOF after the event stream ends; errors are
// returned by Recv, not as Event values. CloseSend signals end-of-input without
// discarding pending recognition results. Calling CloseSend before any
// successful audio upload returns an invalid-argument error without closing.
// Close is idempotent and releases
// resources; canceling the OpenStream context terminates the session.
//
// Contract: after a successful task completion, Recv repeatedly returns io.EOF.
// After a terminal provider/protocol/transport failure, Recv repeatedly returns
// the original failure until the session is discarded. Calling Send on a closed
// stream returns ErrStreamClosed; calling Send after CloseSend returns
// ErrInputClosed. Context cancellation preserves context.Canceled /
// context.DeadlineExceeded rather than becoming a retryable provider failure.
//
// Once CloseSend succeeds, Send returns ErrInputClosed while the session
// remains open for Recv. An explicit Close takes precedence over the half-close
// state: subsequent Send, including an empty Send, returns ErrStreamClosed.
// A canceled Recv terminates its session and subsequent Recv calls retain the
// original context.Canceled or context.DeadlineExceeded cause.
//
// A stream supports one concurrent sender and one concurrent receiver.
// Concurrent Send calls or concurrent Recv calls are not guaranteed safe.
type Stream interface {
	Send(ctx context.Context, pcmOrEncodedAudio []byte) error
	CloseSend(ctx context.Context) error
	Recv(ctx context.Context) (Event, error)
	Close() error
}
