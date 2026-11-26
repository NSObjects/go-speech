package aliyun

import (
	"encoding/json"
	"math"
	"time"

	"github.com/NSObjects/go-speech/tts"
)

// TextIndex is a provider-native character range within a sentence.
type TextIndex struct {
	Begin *int
	End   *int
}

// AlignmentDetails preserves sentence-relative indices and normalized text.
// Indices correspond to the same order as Alignment.Words.
type AlignmentDetails struct {
	SentenceIndex  *int
	NormalizedText string
	Indices        []TextIndex
}

func decodeAlignment(data []byte) (*tts.Alignment, error) {
	var msg struct {
		Payload struct {
			Output struct {
				Type           string `json:"type"`
				OriginalText   string `json:"original_text"`
				NormalizedText string `json:"normalized_text"`
				Sentence       struct {
					Index *int `json:"index"`
					Words []struct {
						Text       string `json:"text"`
						BeginIndex *int   `json:"begin_index"`
						EndIndex   *int   `json:"end_index"`
						BeginTime  *int64 `json:"begin_time"`
						EndTime    *int64 `json:"end_time"`
					} `json:"words"`
				} `json:"sentence"`
			} `json:"output"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, protocol("invalid alignment payload")
	}
	o := msg.Payload.Output
	// Earlier sentence snapshots may repeat or revise word timings. Expose
	// the final snapshot only; sentence-end is not task completion.
	if o.Type != "sentence-end" || len(o.Sentence.Words) == 0 {
		return nil, nil
	}
	a := &tts.Alignment{Text: o.OriginalText}
	details := AlignmentDetails{SentenceIndex: o.Sentence.Index, NormalizedText: o.NormalizedText}
	for _, w := range o.Sentence.Words {
		if w.BeginTime == nil || w.EndTime == nil ||
			*w.BeginTime < 0 || *w.EndTime < *w.BeginTime ||
			*w.EndTime > math.MaxInt64/int64(time.Millisecond) {
			return nil, protocol("invalid word timestamps")
		}
		a.Words = append(a.Words, tts.Word{
			Text: w.Text, StartTime: time.Duration(*w.BeginTime) * time.Millisecond,
			EndTime: time.Duration(*w.EndTime) * time.Millisecond,
		})
		details.Indices = append(details.Indices, TextIndex{Begin: w.BeginIndex, End: w.EndIndex})
	}
	a.Provider = details
	return a, nil
}
