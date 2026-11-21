package volcengine

import (
	"context"
	"testing"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/NSObjects/go-speech/tts"
)

// The V3 API's listed sample rates are a union: unlike PCM and MP3,
// ogg_opus accepts only 48 kHz. Reject unsupported combinations locally.
func TestVolcengineOggOpusRequires48k(t *testing.T) {
	for _, rate := range []int{8000, 16000, 22050, 24000, 32000, 44100} {
		req := tts.Request{
			Text: "你好",
			Voice: "zh_female_vv_uranus_bigtts",
			Format: audio.Format{Encoding: audio.EncodingOpus, SampleRate: rate, Channels: 1},
		}
		_, _, err := validateRequest(req)
		if !speecherr.IsCode(err, speecherr.CodeUnsupported) {
			t.Errorf("Ogg Opus rate=%d: want CodeUnsupported, got %v", rate, err)
		}
	}
	for _, tc := range []struct {
		encoding audio.Encoding
		rate int
		wire string
	}{
		{audio.EncodingOpus, 48000, "ogg_opus"},
		{audio.EncodingPCM16LE, 16000, "pcm"},
		{audio.EncodingPCM16LE, 24000, "pcm"},
		{audio.EncodingMP3, 24000, "mp3"},
		{audio.EncodingMP3, 48000, "mp3"},
	} {
		req := tts.Request{
			Text: "你好",
			Voice: "zh_female_vv_uranus_bigtts",
			Format: audio.Format{Encoding: tc.encoding, SampleRate: tc.rate, Channels: 1},
		}
		got, wire, err := validateRequest(req)
		if err != nil || wire != tc.wire || got.Format != req.Format {
			t.Errorf("format=%+v: got format=%+v, wire=%q, err=%v", req.Format, got.Format, wire, err)
		}
	}
}

func TestVolcengineRejectsUnsupportedOpusBeforeConnecting(t *testing.T) {
	// An unsupported encoding/rate combination must fail before attempting
	// the (deliberately unreachable) loopback endpoint.
	client, err := New(Config{
		APIKey: "test",
		Endpoint: "ws://127.0.0.1:1/api/v3/tts/unidirectional/stream",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.SynthesizeStream(context.Background(), tts.Request{
		Text: "测试",
		Voice: "zh_female_vv_uranus_bigtts",
		Format: audio.Format{Encoding: audio.EncodingOpus, SampleRate: 24000, Channels: 1},
	})
	if !speecherr.IsCode(err, speecherr.CodeUnsupported) {
		t.Fatalf("expected pre-dial format error, got %v", err)
	}
}
