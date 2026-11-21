//go:build integration

package volcengine

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

// TestVolcengineLiveTTS checks true paid Seed TTS v3 audio output and
// SessionFinished status, not ASR accuracy or device playback.
func TestVolcengineLiveTTS(t *testing.T) {
	key := os.Getenv("VOLCENGINE_API_KEY")
	if key == "" {
		t.Fatal("VOLCENGINE_API_KEY secret is required for real Seed TTS")
	}
	resource := os.Getenv("VOLCENGINE_TTS_RESOURCE_ID")
	if resource == "" {
		resource = ResourceSeedTTS2
	}
	voice := os.Getenv("VOLCENGINE_TTS_VOICE")
	if voice == "" {
		voice = "zh_female_shuangkuaisisi_moon_bigtts"
	}
	client, err := New(Config{APIKey: key, ResourceID: resource})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req := tts.Request{
		Text: "你好，欢迎使用语音合成。",
		Voice: voice,
		Format: audio.Format{
			Encoding: audio.EncodingPCM16LE, SampleRate: 24000, Channels: 1,
		},
	}
	s, err := client.SynthesizeStream(ctx, req)
	if err != nil {
		t.Fatalf("Volcengine TTS real cloud connection or text upload: %v", err)
	}
	defer s.Close()
	start := time.Now()
	frames, total := 0, 0
	for {
		chunk, err := s.Recv(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Volcengine TTS real audio result: %v", err)
		}
		if chunk.Format != req.Format || len(chunk.Data) == 0 {
			t.Fatalf("invalid audio chunk format=%+v bytes=%d", chunk.Format, len(chunk.Data))
		}
		frames++
		total += len(chunk.Data)
	}
	t.Logf("Volcengine TTS resource=%s voice=%s PCM24k frames=%d bytes=%d elapsed=%s",
		resource, voice, frames, total, time.Since(start))
	if frames == 0 || total < 1024 || total%2 != 0 {
		t.Fatalf("real Seed TTS produced invalid/empty PCM: frames=%d bytes=%d", frames, total)
	}
	for i := 0; i < 2; i++ {
		if _, err := s.Recv(context.Background()); !errors.Is(err, io.EOF) {
			t.Fatalf("unstable completed stream %d: %v", i, err)
		}
	}
}
