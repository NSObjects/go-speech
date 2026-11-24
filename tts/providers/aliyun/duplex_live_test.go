//go:build integration

package aliyun

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

func TestAliyunLiveDuplex(t *testing.T) {
	key := os.Getenv("DASHSCOPE_API_KEY")
	endpoint := os.Getenv("DASHSCOPE_WS_ENDPOINT")
	if key == "" || endpoint == "" {
		t.Fatal("DASHSCOPE_API_KEY and DASHSCOPE_WS_ENDPOINT are required")
	}
	voice := os.Getenv("DASHSCOPE_TTS_VOICE")
	if voice == "" {
		voice = "longanyang"
	}
	model := os.Getenv("DASHSCOPE_TTS_MODEL")
	client, err := New(Config{APIKey: key, Endpoint: endpoint, Model: model})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	stream, err := client.OpenDuplex(ctx, tts.DuplexRequest{
		Voice: voice,
		Format: audio.Format{
			Encoding: audio.EncodingPCM16LE,
			SampleRate: 22050,
			Channels: 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
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
		total += len(chunk.Data)
	}
	if total < 1024 || total%2 != 0 {
		t.Fatalf("invalid live PCM audio: %d bytes", total)
	}
}
