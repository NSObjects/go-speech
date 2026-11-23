//go:build integration

package volcengine

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/tts"
)

// TestVolcengineLiveBatchFormats exercises the real paid Batch API, rather
// than assuming successful streaming also proves the collecting API works.
func TestVolcengineLiveBatchFormats(t *testing.T) {
	key := os.Getenv("VOLCENGINE_API_KEY")
	if key == "" {
		t.Fatal("VOLCENGINE_API_KEY is required")
	}
	resource := os.Getenv("VOLCENGINE_TTS_RESOURCE_ID")
	if resource == "" {
		resource = ResourceSeedTTS2
	}
	voice := os.Getenv("VOLCENGINE_TTS_VOICE")
	if voice == "" {
		voice = "zh_female_vv_uranus_bigtts"
	}
	client, err := New(Config{APIKey: key, ResourceID: resource})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		format audio.Format
	}{
		{"pcm24k", audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 24000, Channels: 1}},
		{"mp3-24k", audio.Format{Encoding: audio.EncodingMP3, SampleRate: 24000, Channels: 1}},
		{"ogg-opus-48k", audio.Format{Encoding: audio.EncodingOpus, SampleRate: 48000, Channels: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
			defer cancel()
			req := tts.Request{Text: "你好，这是批量语音合成验收。", Voice: voice, Format: tc.format}
			out, err := client.Synthesize(ctx, req)
			if err != nil {
				t.Fatalf("real batch synthesis: %v", err)
			}
			if out.Format != tc.format || len(out.Data) < 1024 || len(out.Data) > 8<<20 {
				t.Fatalf("invalid batch result: format=%+v bytes=%d", out.Format, len(out.Data))
			}
			switch tc.format.Encoding {
			case audio.EncodingPCM16LE:
				if len(out.Data)%2 != 0 {
					t.Fatalf("PCM16LE byte count not aligned: %d", len(out.Data))
				}
			case audio.EncodingMP3:
				isID3 := bytes.HasPrefix(out.Data, []byte("ID3"))
				isMPEG := len(out.Data) >= 2 && out.Data[0] == 0xff && out.Data[1]&0xe0 == 0xe0
				if !isID3 && !isMPEG {
					t.Fatal("MP3 output does not begin with ID3 or MPEG frame")
				}
			case audio.EncodingOpus:
				if !bytes.HasPrefix(out.Data, []byte("OggS")) || !bytes.Contains(out.Data, []byte("OpusHead")) {
					t.Fatal("Ogg Opus output missing OggS/OpusHead")
				}
			}
			t.Logf("real Seed TTS batch %s bytes=%d", tc.name, len(out.Data))
		})
	}
}
