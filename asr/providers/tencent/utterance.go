package tencent

import (
	"encoding/json"
	"math"
	"time"

	"github.com/NSObjects/go-speech/asr"
)

type responseWord struct {
	Text       string `json:"word"`
	StartTime  *int64 `json:"start_time"`
	EndTime    *int64 `json:"end_time"`
	StableFlag *int   `json:"stable_flag"`
}

// UtteranceDetails holds native segment identity and optional word stability.
// RawResult preserves the legacy result object, including undocumented
// provider metadata without assuming a schema for emotion labels.
type UtteranceDetails struct {
	Index       int
	SpeakerID   *int
	StableFlags []*int
	RawResult   json.RawMessage
}

// SessionDetails holds the service-issued speaker context ID (24-hour
// lifetime documented by Tencent). The caller decides whether to reuse it.
type SessionDetails struct {
	SpeakerContextID string
}

var _ asr.SessionMetadataReceiver = (*Stream)(nil)

func (s *Stream) updateSpeakerContext(id string) {
	if id == "" {
		return
	}
	s.metadataMu.Lock()
	defer s.metadataMu.Unlock()
	s.speakerContext = id
}

// SessionMetadata returns a snapshot without blocking a network read.
func (s *Stream) SessionMetadata() any {
	s.metadataMu.Lock()
	defer s.metadataMu.Unlock()
	if s.speakerContext == "" {
		return nil
	}
	return SessionDetails{SpeakerContextID: s.speakerContext}
}

func milliseconds(v *int64) *time.Duration {
	if v == nil || *v < 0 || *v > math.MaxInt64/int64(time.Millisecond) {
		return nil
	}
	d := time.Duration(*v) * time.Millisecond
	return &d
}

func (s *Stream) segmentEvent(kind asr.EventType, r *segment) asr.Event {
	e := asr.Event{Type: kind, Text: r.Text}
	if kind == asr.EventSpeechStart {
		e.Text = ""
	}
	if !s.details {
		return e
	}
	u := &asr.Utterance{
		Text: r.Text, Definite: kind == asr.EventFinal,
		StartTime: milliseconds(r.StartTime), EndTime: milliseconds(r.EndTime),
	}
	d := UtteranceDetails{Index: r.Index, RawResult: r.raw}
	for _, w := range r.Words {
		u.Words = append(u.Words, asr.Word{Text: w.Text, StartTime: milliseconds(w.StartTime), EndTime: milliseconds(w.EndTime)})
		d.StableFlags = append(d.StableFlags, w.StableFlag)
	}
	u.Provider = d
	e.Utterance = u
	return e
}

func (s *Stream) sentenceEvent(kind asr.EventType, r sentence) asr.Event {
	e := asr.Event{Type: kind, Text: r.Text, SpeakerID: r.SpeakerID}
	if s.details {
		e.Utterance = &asr.Utterance{
			Text: r.Text, Definite: kind == asr.EventFinal,
			StartTime: milliseconds(r.StartTime), EndTime: milliseconds(r.EndTime),
			Provider: UtteranceDetails{Index: r.ID, SpeakerID: r.SpeakerID},
		}
	}
	return e
}
