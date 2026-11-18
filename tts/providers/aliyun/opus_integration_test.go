//go:build integration

package aliyun

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/tts"
)

// TestAliyunLiveCosyVoiceOpus verifies actual Ogg Opus returned by CosyVoice.
// It does not assume WebSocket message boundaries coincide with Ogg pages.
func TestAliyunLiveCosyVoiceOpus(t *testing.T) {
	if os.Getenv("DASHSCOPE_API_KEY") == "" || os.Getenv("DASHSCOPE_WS_ENDPOINT") == "" {
		t.Fatal("DASHSCOPE_API_KEY and DASHSCOPE_WS_ENDPOINT are required")
	}
	for _, tool := range []string{"ffprobe", "ffmpeg"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("%s required for actual Ogg Opus verification: %v", tool, err)
		}
	}
	model := os.Getenv("DASHSCOPE_TTS_MODEL")
	if model == "" {
		model = ModelCosyVoiceV3Flash
	}
	voice := os.Getenv("DASHSCOPE_TTS_VOICE")
	if voice == "" {
		voice = "longanyang"
	}
	client, err := New(Config{
		APIKey:   os.Getenv("DASHSCOPE_API_KEY"),
		Endpoint: os.Getenv("DASHSCOPE_WS_ENDPOINT"),
		Model:    model,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := tts.Request{
		Text:   "今天天气很好，我们一起学习。",
		Voice:  voice,
		Format: audio.Format{Encoding: audio.EncodingOpus, SampleRate: 24000, Channels: 1},
	}
	for _, batch := range []bool{false, true} {
		name := "stream"
		if batch {
			name = "batch"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			var encoded []byte
			frames := 0
			started := time.Now()
			firstAudio := time.Duration(0)
			if batch {
				result, err := client.Synthesize(ctx, req)
				if err != nil {
					t.Fatalf("batch Ogg Opus synthesis: %v", err)
				}
				if result.Format != req.Format {
					t.Fatalf("wrong batch output format: %+v", result.Format)
				}
				encoded = result.Data
			} else {
				s, err := client.SynthesizeStream(ctx, req)
				if err != nil {
					t.Fatalf("stream Ogg Opus startup: %v", err)
				}
				defer s.Close()
				for {
					chunk, err := s.Recv(ctx)
					if errors.Is(err, io.EOF) {
						break
					}
					if err != nil {
						t.Fatalf("Ogg Opus audio Recv: %v", err)
					}
					if chunk.Format != req.Format || len(chunk.Data) == 0 {
						t.Fatalf("wrong Ogg Opus chunk format=%+v bytes=%d", chunk.Format, len(chunk.Data))
					}
					if frames == 0 {
						firstAudio = time.Since(started)
					}
					frames++
					encoded = append(encoded, chunk.Data...)
					if len(encoded) > 8<<20 {
						t.Fatal("Ogg Opus response exceeds test limit")
					}
				}
				for i := 0; i < 2; i++ {
					if _, err := s.Recv(context.Background()); !errors.Is(err, io.EOF) {
						t.Fatalf("unstable Opus EOF %d: %v", i, err)
					}
				}
			}
			if len(encoded) < 1024 || !bytes.HasPrefix(encoded, []byte("OggS")) {
				t.Fatalf("expected nonempty Ogg stream, bytes=%d prefix=%q", len(encoded), encoded[:min(len(encoded), 4)])
			}
			headOffset := bytes.Index(encoded[:min(len(encoded), 1024)], []byte("OpusHead"))
			if headOffset < 0 || headOffset+16 > len(encoded) {
				t.Fatal("Ogg Opus stream does not contain OpusHead")
			}
			rateHint := binary.LittleEndian.Uint32(encoded[headOffset+12 : headOffset+16])
			file := filepath.Join(t.TempDir(), "cosyvoice.ogg")
			if err := os.WriteFile(file, encoded, 0600); err != nil {
				t.Fatal(err)
			}
			out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "a:0",
				"-show_entries", "stream=codec_name,sample_rate,channels",
				"-of", "default=noprint_wrappers=1", file).CombinedOutput()
			if err != nil {
				t.Fatalf("ffprobe failed to parse Ogg Opus: %v (%s)", err, out)
			}
			for _, v := range []string{"codec_name=opus", "channels=1"} {
				if !strings.Contains(string(out), v) {
					t.Fatalf("expected %s: actual ffprobe=%s", v, out)
				}
			}
			if out, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-xerror",
				"-i", file, "-f", "null", "-").CombinedOutput(); err != nil {
				t.Fatalf("complete Ogg Opus is not decodable: %v (%s)", err, out)
			}
			t.Logf("real Ogg Opus batch=%v model=%s bytes=%d chunks=%d first_audio=%s elapsed=%s opus_head_rate_hint=%d ffprobe=%s",
				batch, model, len(encoded), frames, firstAudio, time.Since(started),
				rateHint, strings.ReplaceAll(strings.TrimSpace(string(out)), "\n", " "))
		})
	}
}
