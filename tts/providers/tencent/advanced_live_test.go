//go:build integration

package tencent

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/NSObjects/go-speech/tts"
)

func TestTencentLiveAdvanced(t *testing.T) {
	appID, id, key := os.Getenv("TENCENT_APP_ID"), os.Getenv("TENCENT_SECRET_ID"), os.Getenv("TENCENT_SECRET_KEY")
	if appID == "" || id == "" || key == "" {
		t.Fatal("Tencent TTS credentials are required")
	}
	c, err := New(Config{AppID: appID, SecretID: id, SecretKey: key})
	if err != nil {
		t.Fatal(err)
	}
	// Use the documented standard voice, not a configured super-natural
	// voice that may not support timestamps.
	const voice = "101001"
	format := audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1}
	t.Run("controls_and_alignment", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		on, speed, volume := true, 1.0, 1.0
		r, err := tts.SynthesizeDetailed(ctx, c, tts.Request{
			Text: "今天我们来讲一个故事。", Voice: voice, Format: format,
			Options: tts.Options{Provider: Options{Speed: &speed, Volume: &volume, EnableSubtitle: &on}},
		})
		checkAdvancedLiveError(t, err)
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
	t.Run("reset", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		s, err := c.OpenDuplex(ctx, tts.DuplexRequest{Voice: voice, Format: format})
		checkAdvancedLiveError(t, err)
		defer s.Close()
		checkAdvancedLiveError(t, s.SendText(ctx, "这是第一段话。"))
		checkAdvancedLiveError(t, s.SendText(ctx, "这段文字可能仍在缓存中"))
		checkAdvancedLiveError(t, s.(tts.Resetter).Reset(ctx))
		checkAdvancedLiveError(t, s.SendText(ctx, "现在继续合成新的内容。"))
		checkAdvancedLiveError(t, s.CloseSend(ctx))
		var data []byte
		for {
			chunk, err := s.Recv(ctx)
			if errors.Is(err, io.EOF) {
				break
			}
			checkAdvancedLiveError(t, err)
			data = append(data, chunk.Data...)
		}
		verifyAdvancedPCM(t, data)
		if _, err := s.Recv(ctx); !errors.Is(err, io.EOF) {
			t.Fatalf("terminal EOF not stable: %v", err)
		}
		// RESET does not cancel in-progress synthesis. This checks that
		// the session accepts further input and finishes, not which text
		// was already being synthesized at the instant of RESET.
	})
}

func checkAdvancedLiveError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	var remote *speecherr.Error
	if errors.As(err, &remote) && remote.RemoteCode == "3022" && strings.Contains(err.Error(), "The resource pack allowance has been exhausted") {
		t.Skipf("NOT VALIDATED: account resource pack exhausted (3022): %v", err)
	}
	t.Fatal(err)
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
