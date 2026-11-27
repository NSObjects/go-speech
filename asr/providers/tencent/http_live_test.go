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

func TestTencentLiveSentenceRecognition(t *testing.T) {
	c, err := NewHTTP(HTTPConfig{SecretID: os.Getenv("TENCENT_SECRET_ID"), SecretKey: os.Getenv("TENCENT_SECRET_KEY"), EngineModel: "16k_zh"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for _, rate := range []int{16000, 8000} {
		fixture := "TENCENT_TEST_16K_PCM_FILE"
		opts := HTTPOptions{}
		if rate == 8000 {
			fixture = "TENCENT_TEST_8K_PCM_FILE"
			opts.InputSampleRate = &rate
		}
		raw, err := os.ReadFile(os.Getenv(fixture))
		if err != nil {
			t.Fatal(err)
		}
		one := 1
		opts.WordInfo = &one
		res, err := c.Recognize(ctx, audio.Buffer{Data: raw, Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: rate, Channels: 1}}, asr.Config{Options: asr.Options{Provider: opts}})
		if err != nil {
			t.Fatal(err)
		}
		d, ok := res.Provider.(SentenceDetails)
		if !ok || d.RequestID == "" || d.AudioDuration <= 0 || strings.TrimSpace(res.Text) == "" || len(d.WordList) == 0 {
			t.Fatal("missing native sentence result or timestamps")
		}
		t.Logf("real SentenceRecognition: input=%d Hz duration=%s words=%d", rate, d.AudioDuration, len(d.WordList))
	}
}
