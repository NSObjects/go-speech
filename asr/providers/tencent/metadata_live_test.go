//go:build integration

package tencent

import (
	"testing"

	"github.com/NSObjects/go-speech/asr"
)

func verifyTimedUtterances(t *testing.T, utterances []asr.Utterance, requireWords bool) {
	t.Helper()
	segments, words := 0, 0
	for _, u := range utterances {
		if u.StartTime != nil && u.EndTime != nil {
			if *u.StartTime < 0 || *u.EndTime < *u.StartTime {
				t.Fatal("invalid segment timing")
			}
			if *u.EndTime > *u.StartTime {
				segments++
			}
		}
		for _, w := range u.Words {
			if w.StartTime != nil && w.EndTime != nil {
				if *w.StartTime < 0 || *w.EndTime < *w.StartTime {
					t.Fatal("invalid word timing")
				}
				if w.Text != "" && *w.EndTime > *w.StartTime {
					words++
				}
			}
		}
	}
	if segments == 0 || (requireWords && words == 0) {
		t.Fatalf("metadata missing: timed segments=%d timed words=%d (requireWords=%t)", segments, words, requireWords)
	}
	t.Logf("timed segments=%d timed words=%d", segments, words)
}
