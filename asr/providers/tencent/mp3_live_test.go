//go:build integration

package tencent

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
)

func TestTencentLiveNativeMP3(t *testing.T) {
	raw, err := os.ReadFile(os.Getenv("TENCENT_TEST_MP3_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(Config{AppID: os.Getenv("TENCENT_APP_ID"), SecretID: os.Getenv("TENCENT_SECRET_ID"), SecretKey: os.Getenv("TENCENT_SECRET_KEY"), EngineModel: "16k_zh", NeedVAD: true})
	if err != nil {
		t.Fatal(err)
	}
	f := audio.Format{Encoding: audio.EncodingMP3, SampleRate: 16000, Channels: 1}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	result, err := c.Recognize(ctx, audio.Buffer{Data: raw, Format: f}, asr.Config{})
	if err != nil || strings.TrimSpace(result.Text) == "" {
		t.Fatalf("native MP3 recognition: %v", err)
	}
	frames, err := mp3Frames(raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("real Tencent native MP3: %d bytes, %d MPEG frames, %d confirmed characters", len(raw), len(frames), len([]rune(result.Text)))
}
