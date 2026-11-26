package tencent

import (
	"testing"

	"github.com/NSObjects/go-speech/tts"
)

func TestVoiceQuery(t *testing.T) {
	speed, volume, intensity, segment := 1.25, 0.0, 100, 0
	q, err := voiceQuery(tts.Options{Provider: Options{
		Speed: &speed, Volume: &volume, EmotionCategory: "happy",
		EmotionIntensity: &intensity, SegmentRate: &segment,
	}})
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"Speed": "1.25", "Volume": "0", "EmotionCategory": "happy", "EmotionIntensity": "100", "SegmentRate": "0"} {
		if q.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
	if _, err := voiceQuery(tts.Options{Speed: &speed}); err == nil {
		t.Fatal("portable multiplier unexpectedly accepted")
	}
	if q, err := voiceQuery(tts.Options{}); err != nil || len(q) != 0 {
		t.Fatalf("default options = %v, %v", q, err)
	}
}
