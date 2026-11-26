package tencent

import (
	"encoding/json"
	"math"
	"time"

	"github.com/NSObjects/go-speech/tts"
)

// TextIndex is a provider-native character range within the whole input text.
type TextIndex struct {
	Begin *int
	End   *int
}

// AlignmentDetails preserves native text indices in the same order as Words.
type AlignmentDetails struct {
	Indices []TextIndex
}

func decodeAlignment(data []byte) (*tts.Alignment, error) {
	var msg struct {
		Result struct {
			Subtitles []struct {
				Text       string `json:"Text"`
				BeginTime  *int64 `json:"BeginTime"`
				EndTime    *int64 `json:"EndTime"`
				BeginIndex *int   `json:"BeginIndex"`
				EndIndex   *int   `json:"EndIndex"`
				Phoneme    string `json:"Phoneme"`
			} `json:"subtitles"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, protocol("invalid subtitle payload")
	}
	if len(msg.Result.Subtitles) == 0 {
		return nil, nil
	}
	a := &tts.Alignment{}
	details := AlignmentDetails{}
	for _, w := range msg.Result.Subtitles {
		if w.BeginTime == nil || w.EndTime == nil ||
			*w.BeginTime < 0 || *w.EndTime < *w.BeginTime ||
			*w.EndTime > math.MaxInt64/int64(time.Millisecond) {
			return nil, protocol("invalid subtitle timestamps")
		}
		a.Words = append(a.Words, tts.Word{
			Text: w.Text, StartTime: time.Duration(*w.BeginTime) * time.Millisecond,
			EndTime: time.Duration(*w.EndTime) * time.Millisecond, Phoneme: w.Phoneme,
		})
		details.Indices = append(details.Indices, TextIndex{Begin: w.BeginIndex, End: w.EndIndex})
	}
	a.Provider = details
	return a, nil
}
