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
	result, err := client.Recognize(ctx, audio.Buffer{
		Data: raw, Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
	}, asr.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(result.Text) == "" {
		t.Fatal("Volcengine Recognize returned no final text")
	}
	t.Logf("Volcengine short Recognize chars=%d", len([]rune(result.Text)))
}
