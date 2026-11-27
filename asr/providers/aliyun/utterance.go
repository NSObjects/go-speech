package aliyun

import (
	"math"
	"time"

	"github.com/NSObjects/go-speech/asr"
)

type responseWord struct {
	Text        string `json:"text"`
	BeginTime   *int64 `json:"begin_time"`
	EndTime     *int64 `json:"end_time"`
	Punctuation string `json:"punctuation"`
	Fixed       *bool  `json:"fixed"`
}

// WordDetails preserves native punctuation and stability metadata.
type WordDetails struct {
	Punctuation string
	Fixed       *bool
}

// UtteranceDetails preserves provider sentence identity, confidence and
// per-word metadata in the same order as Utterance.Words.
type UtteranceDetails struct {
	SentenceID        *int64
	EmotionConfidence *float64
	Words             []WordDetails
}

func milliseconds(v *int64) *time.Duration {
	if v == nil || *v < 0 || *v > math.MaxInt64/int64(time.Millisecond) {
		return nil
	}
	d := time.Duration(*v) * time.Millisecond
	return &d
}

func (s *Stream) sentenceEvent(kind asr.EventType, sentence responseSentence) asr.Event {
	e := asr.Event{Type: kind, Text: sentence.Text}
	if !s.details {
		return e
	}
	u := &asr.Utterance{
		Text: sentence.Text, Definite: sentence.SentenceEnd,
		StartTime: milliseconds(sentence.BeginTime), EndTime: milliseconds(sentence.EndTime),
	}
	d := UtteranceDetails{SentenceID: sentence.SentenceID}
	if sentence.SentenceEnd {
		u.Emotion = sentence.Emotion
		if v := sentence.Confidence; v != nil && *v >= 0 && *v <= 1 {
			d.EmotionConfidence = v
		}
	}
	for _, w := range sentence.Words {
		u.Words = append(u.Words, asr.Word{Text: w.Text, StartTime: milliseconds(w.BeginTime), EndTime: milliseconds(w.EndTime)})
		d.Words = append(d.Words, WordDetails{Punctuation: w.Punctuation, Fixed: w.Fixed})
	}
	u.Provider = d
	e.Utterance = u
	return e
}
