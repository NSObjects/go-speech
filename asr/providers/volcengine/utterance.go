package volcengine

import (
	"encoding/json"
	"math"
	"strconv"
	"time"

	"github.com/NSObjects/go-speech/asr"
)

// UtteranceDetails preserves native language, gender and other additions.
// Labels are estimates from the service, not verified personal attributes.
type UtteranceDetails struct {
	LanguageLabel string
	GenderLabel   string
	Additions     map[string]json.RawMessage
}

type timedWord struct {
	Text  string `json:"text"`
	Start *int64 `json:"start_time"`
	End   *int64 `json:"end_time"`
}

func duration(ms *int64) *time.Duration {
	if ms == nil || *ms < 0 || *ms > math.MaxInt64/int64(time.Millisecond) {
		return nil
	}
	d := time.Duration(*ms) * time.Millisecond
	return &d
}

func rawString(v json.RawMessage) string {
	var s string
	_ = json.Unmarshal(v, &s)
	return s
}

func rawNumber(v json.RawMessage) *float64 {
	if len(v) == 0 || string(v) == "null" {
		return nil
	}
	var n float64
	if err := json.Unmarshal(v, &n); err != nil {
		var parseErr error
		n, parseErr = strconv.ParseFloat(rawString(v), 64)
		if parseErr != nil {
			return nil
		}
	}
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return nil
	}
	return &n
}

func (u utterance) metadata() *asr.Utterance {
	a := &asr.Utterance{Text: u.Text, Definite: u.Definite}
	if u.startSet {
		a.StartTime = duration(&u.Start)
	}
	if u.endSet {
		a.EndTime = duration(&u.End)
	}
	for _, w := range u.Words {
		a.Words = append(a.Words, asr.Word{Text: w.Text, StartTime: duration(w.Start), EndTime: duration(w.End)})
	}
	if u.Additions != nil {
		a.Emotion = rawString(u.Additions["emotion"])
		a.SpeechRate = rawNumber(u.Additions["speech_rate"])
		a.VolumeDB = rawNumber(u.Additions["volume"])
		a.Provider = UtteranceDetails{
			LanguageLabel: rawString(u.Additions["lid_lang"]),
			GenderLabel:   rawString(u.Additions["gender"]),
			Additions:     u.Additions,
		}
	}
	return a
}
