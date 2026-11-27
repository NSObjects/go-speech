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

func TestTencentLiveDuplex(t *testing.T) {
	appID := os.Getenv("TENCENT_APP_ID")
	secretID := os.Getenv("TENCENT_SECRET_ID")
	secretKey := os.Getenv("TENCENT_SECRET_KEY")
	if appID == "" || secretID == "" || secretKey == "" {
		t.Fatal("Tencent TTS credentials are required")
	}
	voice := os.Getenv("TENCENT_TTS_VOICE")
	if voice == "" {
		voice = "101001"
	}
	client, err := New(Config{
		AppID: appID,
		SecretID: secretID,
		SecretKey: secretKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	start := time.Now()
	stream, err := client.OpenDuplex(ctx, tts.DuplexRequest{
		Voice: voice,
		Format: audio.Format{
			Encoding: audio.EncodingPCM16LE,
			SampleRate: 16000,
			Channels: 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	t.Logf("READY after %s", time.Since(start))
	// Establish the one-shot session before text is ready, as a caller
	// would while ASR/LLM is working. No heartbeat or extra task.
	select {
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-time.After(5 * time.Second):
	}
	start = time.Now()
	if err := stream.SendText(ctx, "你好，"); err != nil {
		t.Fatal(err)
	}
	if err := stream.SendText(ctx, "欢迎使用语音合成。"); err != nil {
		t.Fatal(err)
	}
	if err := stream.CloseSend(ctx); err != nil {
		t.Fatal(err)
	}
	total := 0
	for {
		chunk, err := stream.Recv(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if total == 0 {
			t.Logf("PREOPEN_FIRST_AUDIO after 5s idle, elapsed from text=%s", time.Since(start))
		}
		total += len(chunk.Data)
	}
	if total < 1024 || total%2 != 0 {
		t.Fatalf("invalid live PCM audio: %d bytes", total)
	}
}
