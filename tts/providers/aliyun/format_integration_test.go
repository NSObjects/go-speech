//go:build integration

package aliyun

import (
	"bytes"
	"context"
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

// TestAliyunLiveCosyVoiceEncodedFormats validates the actual output codec,
// sample rate and channel count. Individual WebSocket frames are arbitrary
// slices of one continuous MP3/WAV stream, not independent audio files.
func TestAliyunLiveCosyVoiceEncodedFormats(t *testing.T) {
	if os.Getenv("DASHSCOPE_API_KEY") == "" || os.Getenv("DASHSCOPE_WS_ENDPOINT") == "" {
		t.Fatal("DASHSCOPE_API_KEY and DASHSCOPE_WS_ENDPOINT are required")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Fatalf("ffprobe is required to inspect real encoded audio: %v", err)
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
	for _, tc := range []struct {
		name   string
		format audio.Format
		codec  string
		batch  bool
	}{
		{"mp3-stream", audio.Format{Encoding: audio.EncodingMP3, SampleRate: 22050, Channels: 1}, "mp3", false},
		{"wav-stream", audio.Format{Encoding: audio.EncodingWAV, SampleRate: 22050, Channels: 1}, "pcm_s16le", false},
		{"mp3-batch", audio.Format{Encoding: audio.EncodingMP3, SampleRate: 22050, Channels: 1}, "mp3", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			req := tts.Request{
				Text:   "今天天气很好，我们一起学习。",
				Voice:  voice,
				Format: tc.format,
			}
			var data []byte
			frames := 0
			firstAudio := time.Duration(0)
			start := time.Now()
			if tc.batch {
				buffer, err := client.Synthesize(ctx, req)
				if err != nil {
					t.Fatalf("live batch TTS: %v", err)
				}
				if buffer.Format != tc.format {
					t.Fatalf("wrong batch format: %+v, want %+v", buffer.Format, tc.format)
				}
				data = buffer.Data
			} else {
				stream, err := client.SynthesizeStream(ctx, req)
				if err != nil {
					t.Fatalf("live streaming TTS startup: %v", err)
				}
				defer stream.Close()
				for {
					chunk, err := stream.Recv(ctx)
					if errors.Is(err, io.EOF) {
						break
					}
					if err != nil {
						t.Fatalf("live streaming TTS Recv: %v", err)
					}
					if chunk.Format != tc.format || len(chunk.Data) == 0 {
						t.Fatalf("invalid streaming audio chunk: format=%+v len=%d", chunk.Format, len(chunk.Data))
					}
					if frames == 0 {
						firstAudio = time.Since(start)
					}
					data = append(data, chunk.Data...)
					frames++
					if len(data) > 8<<20 {
						t.Fatal("unexpectedly large live synthesis")
					}
				}
				for i := 0; i < 2; i++ {
					if _, err := stream.Recv(context.Background()); !errors.Is(err, io.EOF) {
						t.Fatalf("unstable EOF after completed TTS: %v", err)
					}
				}
			}
			if len(data) < 1024 {
				t.Fatalf("encoded output is too short: %d bytes", len(data))
			}
			if tc.format.Encoding == audio.EncodingWAV {
				if len(data) < 12 || !bytes.Equal(data[:4], []byte("RIFF")) ||
					!bytes.Equal(data[8:12], []byte("WAVE")) {
					t.Fatalf("WAV stream missing RIFF/WAVE file header")
				}
			}
			ext := ".mp3"
			if tc.format.Encoding == audio.EncodingWAV {
				ext = ".wav"
			}
			file := filepath.Join(t.TempDir(), "cosyvoice"+ext)
			if err := os.WriteFile(file, data, 0600); err != nil {
				t.Fatal(err)
			}
			probe := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "a:0",
				"-show_entries", "stream=codec_name,sample_rate,channels",
				"-of", "default=noprint_wrappers=1", file)
			info, err := probe.CombinedOutput()
			if err != nil {
				t.Fatalf("ffprobe rejected generated %s: %v (%s)", tc.name, err, strings.TrimSpace(string(info)))
			}
			for _, want := range []string{"codec_name=" + tc.codec, "sample_rate=22050", "channels=1"} {
				if !strings.Contains(string(info), want) {
					t.Fatalf("real audio format mismatch: expected %q; ffprobe=%s", want, info)
				}
			}
			t.Logf("real CosyVoice format=%s model=%s bytes=%d chunks=%d first_audio=%s elapsed=%s ffprobe=%s",
				tc.name, model, len(data), frames, firstAudio, time.Since(start),
				strings.ReplaceAll(strings.TrimSpace(string(info)), "\n", " "))
		})
	}
}
