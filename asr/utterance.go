package asr

import "time"

// Utterance describes one recognized segment. Times are audio-relative.
// Missing metadata is nil, not zero. Definite is the provider's own finality
// flag, which may differ from EventFinal at the end of an input stream.
type Utterance struct {
	Text      string
	Definite  bool
	StartTime *time.Duration
	EndTime   *time.Duration
	Words     []Word
	// Emotion is a provider-reported label, not a verified user state.
	Emotion string
	// SpeechRate is in provider tokens per second, when available.
	SpeechRate *float64
	// VolumeDB is provider-reported volume in decibels, when available.
	VolumeDB *float64
	// Provider holds native metadata without cross-provider reinterpretation.
	Provider any
}

// Word is a provider-reported timed word or character.
type Word struct {
	Text      string
	StartTime *time.Duration
	EndTime   *time.Duration
}
