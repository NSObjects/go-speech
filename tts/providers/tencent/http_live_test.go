//go:build integration

package tencent

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/tts"
)

func TestTencentLiveHTTPTextToVoice(t *testing.T) {
	c, err := NewHTTP(HTTPConfig{SecretID: os.Getenv("TENCENT_SECRET_ID"), SecretKey: os.Getenv("TENCENT_SECRET_KEY")})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	yes := true
	r, err := c.SynthesizeDetailed(ctx, tts.Request{
		// 101001 supports SSML and 16 kHz in the official basic synthesis voice list.
		Text: `<speak>你好<break time="500ms"/>欢迎使用语音合成。</speak>`,
		Voice: "101001", TextFormat: tts.TextSSML,
		Options: tts.Options{Provider: HTTPOptions{Options: Options{EnableSubtitle: &yes}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := audio.DecodeWAVPCM16(r.Audio.Data)
	if err != nil || len(b.Data) < 3200 || b.Format.SampleRate != 16000 || b.Format.Channels != 1 {
		t.Fatal("invalid native WAV response")
	}
	if len(r.Alignments) == 0 || len(r.Subtitles) == 0 {
		t.Fatal("subtitle request returned no alignment")
	}
	t.Logf("real TextToVoice returned %d WAV bytes and %d subtitles", len(r.Audio.Data), len(r.Subtitles))

	// Verify the two remaining native codecs with short independent requests.
	for _, encoding := range []audio.Encoding{audio.EncodingPCM16LE, audio.EncodingMP3} {
		t.Run(string(encoding), func(t *testing.T) {
			f := audio.Format{Encoding: encoding, SampleRate: 16000, Channels: 1}
			got, err := c.Synthesize(ctx, tts.Request{Text: "你好，语音测试。", Voice: "101001", Format: f})
			if err != nil {
				t.Fatal(err)
			}
			if got.Format != f || len(got.Data) < 512 {
				t.Fatal("empty or mismatched native audio")
			}
			if err := audio.ValidateBuffer(got); err != nil {
				t.Fatal(err)
			}
			if encoding == audio.EncodingPCM16LE {
				// Headerless PCM cannot be identified by magic bytes. Reject
				// recognizable encoded containers instead of silently relabeling.
				if kind, err := audio.Detect(got.Data); err == nil && kind == audio.EncodingWAV {
					t.Fatal("PCM request returned WAV container")
				}
			}
			t.Logf("real TextToVoice returned %d %s bytes", len(got.Data), encoding)
		})
	}
}
