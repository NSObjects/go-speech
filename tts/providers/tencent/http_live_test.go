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
}
