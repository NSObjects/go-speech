package volcengine

import (
	"encoding/json"
	"testing"

	"github.com/NSObjects/go-speech/tts"
)

func TestVoiceSettings(t *testing.T) {
	speed, volume, pitch, boost := 1.2, 0.8, 0, false
	s, err := settings(tts.Options{
		Speed: &speed, Instruction: "温柔",
		Provider: Options{Volume: &volume, Pitch: &pitch, SimilarityBoost: &boost},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"speed":1.2,"volume":0.8,"pitch":0,"instructions":["温柔"],"similarity_boost":false}`
	if string(b) != want {
		t.Fatalf("voice settings = %s, want %s", b, want)
	}
	if s, err := settings(tts.Options{}); err != nil || s != nil {
		t.Fatalf("default settings = %v, %v", s, err)
	}
	speed = 2.1
	if _, err := settings(tts.Options{Speed: &speed}); err == nil {
		t.Fatal("out of range speed accepted")
	}
}
