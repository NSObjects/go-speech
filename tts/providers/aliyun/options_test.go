package aliyun

import (
	"testing"

	"github.com/NSObjects/go-speech/tts"
)

func TestVoiceParameters(t *testing.T) {
	speed, pitch, volume, seed := 1.2, 0.8, 0, 0
	params, err := voiceParameters(tts.Options{
		Speed: &speed, Instruction: "温柔",
		Provider: Options{Volume: &volume, Pitch: &pitch, Seed: &seed},
	})
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]any{"rate": speed, "pitch": pitch, "volume": volume, "seed": seed, "instruction": "温柔"} {
		if params[k] != want {
			t.Errorf("%s = %v, want %v", k, params[k], want)
		}
	}
	if p, err := voiceParameters(tts.Options{}); err != nil || len(p) != 0 {
		t.Fatalf("default options = %v, %v", p, err)
	}
	volume = 101
	if _, err := voiceParameters(tts.Options{Provider: Options{Volume: &volume}}); err == nil {
		t.Fatal("out of range volume accepted")
	}
}
