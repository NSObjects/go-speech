//go:build integration

package volcengine

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/tts"
)

func TestVolcengineLiveSeed3(t *testing.T) {
	key, voice := os.Getenv("VOLCENGINE_API_KEY"), os.Getenv("VOLCENGINE_TTS_VOICE")
	if key == "" || voice == "" {
		t.Skip("set VOLCENGINE_API_KEY and authorized Seed-TTS 3.0 VOLCENGINE_TTS_VOICE")
	}
	c, err := New(Config{APIKey: key, ModelID: os.Getenv("VOLCENGINE_TTS_MODEL_ID"), LanguageCode: "zh"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	format := audio.Format{Encoding: audio.EncodingOpus, SampleRate: 48000, Channels: 1}
	out, err := c.Synthesize(ctx, tts.Request{Text: "你好，欢迎使用语音合成。", Voice: voice, Format: format})
	if err != nil {
		t.Fatal(err)
	}
	verifyLiveOpus(t, out.Data)
	s, err := c.OpenDuplex(ctx, tts.DuplexRequest{Voice: voice, Format: format})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	send := make(chan error, 1)
	go func() {
		if err := s.SendText(ctx, "你好，"); err != nil {
			send <- err
			return
		}
		if err := s.SendText(ctx, "今天我们来讲个故事。"); err != nil {
			send <- err
			return
		}
		send <- s.CloseSend(ctx)
	}()
	var data []byte
	for {
		chunk, err := s.Recv(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, chunk.Data...)
	}
	if err := <-send; err != nil {
		t.Fatal(err)
	}
	verifyLiveOpus(t, data)
}

func verifyLiveOpus(t *testing.T, data []byte) {
	t.Helper()
	if len(data) < 100 || string(data[:4]) != "OggS" {
		t.Fatal("provider did not return Ogg Opus")
	}
	path := t.TempDir() + "/live.ogg"
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Fatal("ffmpeg required to verify live output")
	}
	cmd := exec.Command("ffmpeg", "-v", "error", "-xerror", "-i", path, "-f", "null", "-")
	if result, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("decode failed: %s: %v", result, err)
	}
}
