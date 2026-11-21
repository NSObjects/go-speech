//go:build integration

package volcengine

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/tts"
)

// Verify the encoded output is a single decodable bitstream, not just a
// successful WebSocket exchange. This is real and billable cloud synthesis.
func TestVolcengineLiveEncodedFormats(t *testing.T) {
	apiKey := os.Getenv("VOLCENGINE_API_KEY")
	if apiKey == "" {
		t.Fatal("VOLCENGINE_API_KEY is required for live TTS validation")
	}
	resource := os.Getenv("VOLCENGINE_TTS_RESOURCE_ID")
	if resource == "" {
		resource = ResourceSeedTTS2
	}
	voice := os.Getenv("VOLCENGINE_TTS_VOICE")
	if voice == "" {
		voice = "zh_female_vv_uranus_bigtts"
	}
	client, err := New(Config{APIKey: apiKey, ResourceID: resource})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		format audio.Format
	}{
		{
			name: "mp3-24k",
			format: audio.Format{Encoding: audio.EncodingMP3, SampleRate: 24000, Channels: 1},
		},
		{
			name: "ogg-opus-48k",
			format: audio.Format{Encoding: audio.EncodingOpus, SampleRate: 48000, Channels: 1},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			request := tts.Request{Text: "你好，这是语音合成格式测试。", Voice: voice, Format: tc.format}
			stream, err := client.SynthesizeStream(ctx, request)
			if err != nil {
				t.Fatalf("start real %s synthesis: %v", tc.name, err)
			}
			defer stream.Close()
			var combined []byte
			chunks := 0
			for {
				chunk, err := stream.Recv(ctx)
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatalf("real %s audio Recv: %v", tc.name, err)
				}
				if len(chunk.Data) == 0 || chunk.Format != request.Format {
					t.Fatalf("bad %s audio chunk: format=%+v bytes=%d", tc.name, chunk.Format, len(chunk.Data))
				}
				if len(chunk.Data) > (8<<20)-len(combined) {
					t.Fatalf("%s audio exceeded 8 MiB test budget", tc.name)
				}
				combined = append(combined, chunk.Data...)
				chunks++
			}
			if chunks == 0 || len(combined) < 1024 {
				t.Fatalf("%s yielded no meaningful audio: chunks=%d bytes=%d", tc.name, chunks, len(combined))
			}
			switch tc.format.Encoding {
			case audio.EncodingOpus:
				if !bytes.HasPrefix(combined, []byte("OggS")) {
					t.Fatalf("Ogg Opus stream does not begin with an OggS page")
				}
				if !bytes.Contains(combined, []byte("OpusHead")) {
					t.Fatalf("Ogg Opus stream missing OpusHead")
				}
			case audio.EncodingMP3:
				isID3 := bytes.HasPrefix(combined, []byte("ID3"))
				isMPEG := len(combined) >= 2 && combined[0] == 0xff && combined[1]&0xe0 == 0xe0
				if !isID3 && !isMPEG {
					t.Fatalf("MP3 stream missing ID3 or MPEG audio frame header")
				}
			}
			for i := 0; i < 2; i++ {
				if _, err := stream.Recv(context.Background()); !errors.Is(err, io.EOF) {
					t.Fatalf("%s EOF %d is not stable: %v", tc.name, i, err)
				}
			}
			t.Logf("real Volcengine TTS %s voice=%s chunks=%d bytes=%d", tc.name, voice, chunks, len(combined))
		})
	}
}
