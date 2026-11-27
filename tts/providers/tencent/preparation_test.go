package tencent

import (
	"testing"

	"github.com/NSObjects/go-speech/tts"
)

func TestPreparedOptionsSnapshot(t *testing.T) {
	c, err := New(Config{AppID: "1", SecretID: "id", SecretKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	v := 1.0
	on := true
	req := tts.Request{Text: "hello", Voice: "101001", Options: tts.Options{
		Provider: &Options{Volume: &v, EnableSubtitle: &on},
	}}
	_, _, voice, err := c.prepareRequest(req, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := c.prepareRequest(req, true); err == nil {
		t.Fatal("audio-only batch accepted alignment option")
	}
	v = 0
	on = false
	if voice.Get("Volume") != "1" || voice.Get("EnableSubtitle") != "true" {
		t.Fatalf("prepared options changed: %v", voice)
	}
	if err := c.ValidateSynthesize(req); err != nil {
		t.Fatalf("explicitly disabled alignment rejected: %v", err)
	}
}
