//go:build integration

package tencent

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/tts"
)

// TestTencentLiveTTS verifies the real, billable streaming TTS API. ASR API
// health cannot substitute for a TTS entitlement or successful audio output.
func TestTencentLiveTTS(t *testing.T) {
	appID := os.Getenv("TENCENT_APP_ID")
	secretID := os.Getenv("TENCENT_SECRET_ID")
	secretKey := os.Getenv("TENCENT_SECRET_KEY")
	if appID == "" || secretID == "" || secretKey == "" {
		t.Fatal("TENCENT_APP_ID, TENCENT_SECRET_ID and TENCENT_SECRET_KEY are required")
	}
	voice := os.Getenv("TENCENT_TTS_VOICE")
	if voice == "" {
		voice = "101001"
	}
	client, err := New(Config{
		AppID: appID, SecretID: secretID, SecretKey: secretKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req := tts.Request{
		Text: "你好，欢迎使用语音合成。",
		Voice: voice,
		Format: audio.Format{
			Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1,
		},
	}
	stream, err := client.SynthesizeStream(ctx, req)
	if err != nil {
		t.Fatalf("Tencent TTS real cloud handshake or READY failed: %v", err)
	}
	defer stream.Close()
	start := time.Now()
	chunks, total := 0, 0
	for {
		chunk, err := stream.Recv(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Tencent TTS real audio stream: %v", err)
		}
		if chunk.Format != req.Format || len(chunk.Data) == 0 {
			t.Fatalf("invalid audio chunk format=%+v bytes=%d", chunk.Format, len(chunk.Data))
		}
		chunks++
		total += len(chunk.Data)
	}
	t.Logf("Tencent TTS voice=%s PCM16k chunks=%d bytes=%d elapsed=%s",
		voice, chunks, total, time.Since(start))
	if chunks == 0 || total < 1024 || total%2 != 0 {
		t.Fatalf("real Tencent TTS produced invalid/empty PCM: frames=%d bytes=%d", chunks, total)
	}
	for i := 0; i < 2; i++ {
		if _, err := stream.Recv(context.Background()); !errors.Is(err, io.EOF) {
			t.Fatalf("unstable completed stream %d: %v", i, err)
		}
	}
}
