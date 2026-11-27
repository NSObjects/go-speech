//go:build integration

package aliyun

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/tts"
)

func TestAliyunLiveAdvanced(t *testing.T) {
	key, endpoint := os.Getenv("DASHSCOPE_API_KEY"), os.Getenv("DASHSCOPE_WS_ENDPOINT")
	if key == "" || endpoint == "" {
		t.Fatal("DASHSCOPE_API_KEY and DASHSCOPE_WS_ENDPOINT are required")
	}
	// The documented longanyang system voice on v3-flash supports SSML
	// and timestamps. Do not assume the application's configured voice does.
	c, err := New(Config{APIKey: key, Endpoint: endpoint, Model: "cosyvoice-v3-flash"})
	if err != nil {
		t.Fatal(err)
	}
	const voice = "longanyang"
	format := audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 22050, Channels: 1}
	t.Run("controls_and_alignment", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		on, speed, pitch, volume := true, 1.1, 1.05, 55
		r, err := tts.SynthesizeDetailed(ctx, c, tts.Request{
			Text: "今天我们来讲一个故事。", Voice: voice, Format: format,
			Options: tts.Options{Speed: &speed, Provider: Options{
				Pitch: &pitch, Volume: &volume, WordTimestampEnabled: &on,
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if r.Audio.Format != format {
			t.Fatalf("audio format = %+v", r.Audio.Format)
		}
		verifyAdvancedPCM(t, r.Audio.Data)
		n := 0
		for _, a := range r.Alignments {
			for _, w := range a.Words {
				if w.Text == "" || w.StartTime < 0 || w.EndTime < w.StartTime {
					t.Fatalf("invalid alignment: %+v", w)
				}
				if w.EndTime > w.StartTime {
					n++
				}
			}
		}
		if n == 0 {
			t.Fatal("subtitle requested but no positive-duration word timing returned")
		}
		t.Logf("provider returned %d timed words and %d PCM bytes", n, len(r.Audio.Data))
	})
	t.Run("ssml", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		r, err := c.Synthesize(ctx, tts.Request{
			Text: "<speak>你好。<break time=\"200ms\"/>今天见到你很高兴。</speak>",
			Voice: voice, Format: format, TextFormat: tts.TextSSML,
		})
		if err != nil {
			t.Fatal(err)
		}
		verifyAdvancedPCM(t, r.Data)
	})
	t.Run("cancel", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		s, err := c.OpenDuplex(ctx, tts.DuplexRequest{Voice: voice, Format: format})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if err := s.SendText(ctx, "这是第一句话，接下来我们还有很多故事要讲。"); err != nil {
			t.Fatal(err)
		}
		// Cancel an actual active synthesis, not just an empty task.
		if chunk, err := s.Recv(ctx); err != nil || len(chunk.Data) == 0 {
			t.Fatalf("initial audio: %v", err)
		}
		if err := s.(tts.Canceler).Cancel(ctx); err != nil {
			t.Fatal(err)
		}
		for {
			_, err := s.Recv(ctx)
			if errors.Is(err, tts.ErrSynthesisCanceled) {
				break
			}
			if err != nil {
				t.Fatalf("cancel confirmation: %v", err)
			}
		}
		if _, err := s.Recv(ctx); !errors.Is(err, tts.ErrSynthesisCanceled) {
			t.Fatalf("cancellation not stable: %v", err)
		}
	})
}

func verifyAdvancedPCM(t *testing.T, data []byte) {
	t.Helper()
	if len(data) < 1024 || len(data)%2 != 0 {
		t.Fatalf("invalid PCM length: %d", len(data))
	}
	for _, b := range data {
		if b != 0 {
			return
		}
	}
	t.Fatal("PCM is entirely zero")
}
