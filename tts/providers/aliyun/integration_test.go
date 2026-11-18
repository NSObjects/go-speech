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

// Real, billable CosyVoice synthesis. No speaker, playback or device-specific
// framing is involved. It verifies first binary audio, format, and clean EOF.
func TestAliyunLiveCosyVoice(t *testing.T) {
	if os.Getenv("DASHSCOPE_API_KEY") == "" || os.Getenv("DASHSCOPE_WS_ENDPOINT") == "" {
		t.Fatal("DASHSCOPE_API_KEY and DASHSCOPE_WS_ENDPOINT are required")
	}
	model := os.Getenv("DASHSCOPE_TTS_MODEL")
	if model == "" { model = ModelCosyVoiceV3Flash }
	voice := os.Getenv("DASHSCOPE_TTS_VOICE")
	if voice == "" { voice = "longanyang" }
	client, err := New(Config{
		Endpoint: os.Getenv("DASHSCOPE_WS_ENDPOINT"),
		APIKey: os.Getenv("DASHSCOPE_API_KEY"),
		Model: model,
	})
	if err != nil { t.Fatal(err) }
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	request := tts.Request{
		Text: "你好，欢迎使用语音合成。",
		Voice: voice,
		Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 22050, Channels: 1},
	}
	s, err := client.SynthesizeStream(ctx, request)
	if err != nil { t.Fatalf("real CosyVoice task startup: %v", err) }
	defer s.Close()
	start := time.Now()
	frames, total := 0, 0
	firstAudio := time.Duration(0)
	for {
		chunk, err := s.Recv(ctx)
		if errors.Is(err, io.EOF) { break }
		if err != nil { t.Fatalf("real CosyVoice audio Recv: %v", err) }
		if chunk.Format != request.Format || len(chunk.Data) == 0 {
			t.Fatalf("invalid TTS chunk format=%+v bytes=%d", chunk.Format, len(chunk.Data))
		}
		if frames == 0 { firstAudio = time.Since(start) }
		frames++
		total += len(chunk.Data)
	}
	t.Logf("live CosyVoice model=%s voice=%s format=pcm rate=22050 frames=%d bytes=%d first_audio_after_stream_open=%s elapsed=%s",
		model, voice, frames, total, firstAudio, time.Since(start))
	if frames == 0 || total < 1024 { t.Fatalf("no meaningful PCM audio generated: %d frames %d bytes", frames, total) }
	if total%2 != 0 { t.Fatalf("16-bit PCM output has odd byte length: %d", total) }
	for i := 0; i < 2; i++ {
		if _, err := s.Recv(context.Background()); !errors.Is(err, io.EOF) { t.Fatalf("unstable EOF: %v", err) }
	}
}
