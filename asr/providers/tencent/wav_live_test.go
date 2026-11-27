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

func TestTencentLiveWAVRecognize(t *testing.T) {
	raw, err := os.ReadFile(os.Getenv("TENCENT_TEST_16K_PCM_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	wav, err := audio.EncodeWAVPCM16(audio.Buffer{Data: raw, Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1}})
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(Config{AppID: os.Getenv("TENCENT_APP_ID"), SecretID: os.Getenv("TENCENT_SECRET_ID"), SecretKey: os.Getenv("TENCENT_SECRET_KEY"), EngineModel: "16k_zh", NeedVAD: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	res, err := c.Recognize(ctx, wav, asr.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(res.Text) == "" {
		t.Fatal("native WAV recognition returned no confirmed text")
	}
	t.Logf("real Tencent native WAV WebSocket recognized %d bytes, %d characters", len(wav.Data), len([]rune(res.Text)))
}
