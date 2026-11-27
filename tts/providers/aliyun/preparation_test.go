package aliyun

import (
	"testing"

	"github.com/NSObjects/go-speech/tts"
)

func TestPreparedOptionsSnapshot(t *testing.T) {
	c, err := New(Config{APIKey: "test"})
	if err != nil {
		t.Fatal(err)
	}
	v := 55
	on := true
	req := tts.Request{Text: "hello", Voice: "longanyang", Options: tts.Options{
		Provider: &Options{Volume: &v, WordTimestampEnabled: &on},
	}}
	_, _, params, err := c.prepareRequest(req, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := c.prepareRequest(req, true); err == nil {
		t.Fatal("audio-only batch accepted alignment option")
	}
	v = 0
	on = false
	if params["volume"] != 55 || params["word_timestamp_enabled"] != true {
		t.Fatalf("prepared options changed: %v", params)
	}
	if err := c.ValidateSynthesize(req); err != nil {
		t.Fatalf("explicitly disabled alignment rejected: %v", err)
	}
}
