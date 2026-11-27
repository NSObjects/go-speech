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

func TestVolcengineLiveAdvanced(t *testing.T) {
	key, voice := os.Getenv("VOLCENGINE_API_KEY"), os.Getenv("VOLCENGINE_TTS_VOICE")
	if key == "" || voice == "" {
		t.Fatal("VOLCENGINE_API_KEY and VOLCENGINE_TTS_VOICE are required")
	}
	c, err := New(Config{APIKey: key, ModelID: os.Getenv("VOLCENGINE_TTS_MODEL_ID"), LanguageCode: "zh"})
	if err != nil {
		t.Fatal(err)
	}
	format := audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 24000, Channels: 1}
	t.Run("controls_and_alignment", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		on, speed, volume, pitch := true, 1.1, 0.9, 1
		r, err := tts.SynthesizeDetailed(ctx, c, tts.Request{
			Text: "今天我们来讲一个故事。", Voice: voice, Format: format,
			Options: tts.Options{Speed: &speed, Provider: Options{
				Volume: &volume, Pitch: &pitch, EnableSubtitle: &on,
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
	t.Run("inline_tag_and_flush", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
		defer cancel()
		s, err := c.OpenDuplex(ctx, tts.DuplexRequest{Voice: voice, Format: format, TextFormat: tts.TextInlineTag})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		reader := s.(tts.EventReceiver)
		if err := s.SendText(ctx, "[happy]我们终于成功了！"); err != nil {
			t.Fatal(err)
		}
		if err := s.(tts.Flusher).Flush(ctx); err != nil {
			t.Fatal(err)
		}
		var first []byte
		for {
			e, err := reader.RecvEvent(ctx)
			if err != nil {
				t.Fatalf("before FLUSHED: %v", err)
			}
			if e.Type == tts.EventBatchCompleted {
				break
			}
			if e.Type == tts.EventAudio {
				first = append(first, e.Audio.Data...)
			}
		}
		verifyAdvancedPCM(t, first)
		// Split a single tag across writes like an incremental LLM output.
		// Successful synthesis demonstrates transport acceptance, not correct
		// interpretation of the tag or a verified emotional delivery.
		for _, part := range []string{"[sa", "d]", "不过今天就要说再见了。"} {
			if err := s.SendText(ctx, part); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.CloseSend(ctx); err != nil {
			t.Fatal(err)
		}
		var second []byte
		for {
			e, err := reader.RecvEvent(ctx)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if e.Type == tts.EventAudio {
				second = append(second, e.Audio.Data...)
			}
		}
		verifyAdvancedPCM(t, second)
		if _, err := reader.RecvEvent(ctx); !errors.Is(err, io.EOF) {
			t.Fatalf("terminal EOF not stable: %v", err)
		}
		t.Logf("FLUSHED followed by another synthesis and CLOSED: %d / %d bytes", len(first), len(second))
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
