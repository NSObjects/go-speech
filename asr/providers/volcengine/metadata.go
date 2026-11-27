package volcengine

import (
	"crypto/sha256"
	"encoding/json"

	"github.com/NSObjects/go-speech/asr"
)

// Store only a digest of the last emitted snapshot, not an additional
// copy of words/additions for every utterance. Text is retained to reject
// revisions that cannot safely be correlated with immutable final text.
type finalMetadata struct {
	text   string
	digest [sha256.Size]byte
}

func detailedEvent(kind asr.EventType, text string, u *asr.Utterance) asr.Event {
	e := asr.Event{Type: kind, Text: text, Utterance: u}
	if u != nil {
		e.SpeakerID = u.SpeakerID
	}
	return e
}

// Timed intervals are the only stable identity available in the documented
// protocol. Do not correlate untimed text or changed segmentation.
func (u utterance) hasStableInterval() bool {
	return u.startSet && u.endSet && u.Start >= 0 && u.End > u.Start &&
		duration(&u.Start) != nil && duration(&u.End) != nil
}

func metadataDigest(u utterance) [sha256.Size]byte {
	// These fields are decoded from JSON, so serialization cannot fail.
	b, _ := json.Marshal(u)
	return sha256.Sum256(b)
}

func (s *Stream) rememberFinalMetadata(key string, u utterance, a *asr.Utterance) {
	if !u.hasStableInterval() {
		return
	}
	if s.finalMetadata == nil {
		s.finalMetadata = make(map[string]finalMetadata)
	}
	a.ID = key
	s.finalMetadata[key] = finalMetadata{text: u.Text, digest: metadataDigest(u)}
}

func (s *Stream) updateFinalMetadata(key string, u utterance) {
	previous, ok := s.finalMetadata[key]
	if !ok || !u.hasStableInterval() || previous.text != u.Text {
		return
	}
	// Empty metadata is not an update; it may be an abbreviated replay.
	if len(u.Words) == 0 && len(u.Additions) == 0 {
		return
	}
	digest := metadataDigest(u)
	if previous.digest == digest {
		return
	}
	a := u.metadata()
	a.ID = key
	s.finalMetadata[key] = finalMetadata{text: u.Text, digest: digest}
	// The update is a provider snapshot, not an appended transcript.
	s.queue = append(s.queue, detailedEvent(asr.EventUtteranceUpdate, "", a))
}
