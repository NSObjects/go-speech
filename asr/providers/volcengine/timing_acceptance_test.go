package volcengine

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
)

// A pinned single-utterance live fixture can receive a definite two-pass
// result without start_time. Accept only that observed wire shape; do not
// synthesize a segment start or accept missing/invalid word timestamps.
func singleFinalWithoutStart(utterances []asr.Utterance) bool {
	if len(utterances) != 1 {
		return false
	}
	u := utterances[0]
	if !u.Definite || u.Text == "" || u.StartTime != nil || u.EndTime == nil || *u.EndTime <= 0 || len(u.Words) == 0 {
		return false
	}
	for _, w := range u.Words {
		if w.Text == "" || w.StartTime == nil || w.EndTime == nil ||
			*w.StartTime < 0 || *w.EndTime <= *w.StartTime || *w.EndTime > *u.EndTime {
			return false
		}
	}
	return true
}

func TestLiveTimingAcceptance(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want bool
	}{
		{"observed missing start", `{"text":"腾讯","definite":true,"end_time":3052,"words":[{"text":"腾","start_time":120,"end_time":320},{"text":"讯","start_time":320,"end_time":480}]}`, true},
		{"explicit zero uses strict path", `{"text":"腾讯","definite":true,"start_time":0,"end_time":3052,"words":[{"text":"腾","start_time":120,"end_time":320}]}`, false},
		{"missing end", `{"text":"腾讯","definite":true,"words":[{"text":"腾","start_time":120,"end_time":320}]}`, false},
		{"no words", `{"text":"腾讯","definite":true,"end_time":3052}`, false},
		{"invalid word interval", `{"text":"腾讯","definite":true,"end_time":3052,"words":[{"text":"腾","start_time":320,"end_time":120}]}`, false},
		{"word outside segment end", `{"text":"腾讯","definite":true,"end_time":300,"words":[{"text":"腾","start_time":120,"end_time":320}]}`, false},
		{"provisional result", `{"text":"腾讯","definite":false,"end_time":3052,"words":[{"text":"腾","start_time":120,"end_time":320}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var native utterance
			if err := json.Unmarshal([]byte(tc.raw), &native); err != nil {
				t.Fatal(err)
			}
			u := native.metadata()
			if got := singleFinalWithoutStart([]asr.Utterance{*u}); got != tc.want {
				t.Fatalf("accepted=%t, want %t: %+v", got, tc.want, u)
			}
			if tc.want && (u.StartTime != nil || u.EndTime == nil || *u.EndTime != 3052*time.Millisecond) {
				t.Fatalf("provider timing changed: %+v", u)
			}
			if tc.name == "explicit zero uses strict path" && (u.StartTime == nil || *u.StartTime != 0) {
				t.Fatal("explicit zero timestamp lost")
			}
			if tc.want && singleFinalWithoutStart([]asr.Utterance{*u, *u}) {
				t.Fatal("multiple segments must not use the single-utterance exception")
			}
		})
	}
}
