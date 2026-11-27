//go:build integration

package volcengine

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
)

func TestVolcengineLiveShortRecognize(t *testing.T) {
	path := os.Getenv("VOLCENGINE_TEST_PCM_FILE")
	if path == "" {
		t.Fatal("VOLCENGINE_TEST_PCM_FILE is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(Config{
		APIKey:     os.Getenv("VOLCENGINE_API_KEY"),
		ResourceID: os.Getenv("VOLCENGINE_RESOURCE_ID"),
		Endpoint:   os.Getenv("VOLCENGINE_WS_ENDPOINT"),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	on := true
	result, err := client.Recognize(ctx, audio.Buffer{
		Data: raw, Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
	}, asr.Config{Options: asr.Options{UtteranceDetails: true, Provider: Options{
		EnableNonStream: &on, ShowSpeechRate: &on, ShowVolume: &on, EnableEmotionDetection: &on,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(result.Text) == "" {
		t.Fatal("Volcengine Recognize returned no final text")
	}
	verifyTimedUtterances(t, result.Utterances, true)
	rate, volume, emotion := 0, 0, 0
	for _, u := range result.Utterances {
		if u.SpeechRate != nil && *u.SpeechRate > 0 {
			rate++
		}
		if u.VolumeDB != nil {
			volume++
		}
		if u.Emotion != "" {
			emotion++
		}
	}
	if rate == 0 || volume == 0 || emotion == 0 {
		t.Fatalf("requested perception metadata missing: speech_rate=%d volume=%d emotion=%d", rate, volume, emotion)
	}
	t.Logf("Volcengine short Recognize chars=%d speech_rate=%d volume=%d emotion=%d", len([]rune(result.Text)), rate, volume, emotion)
}
