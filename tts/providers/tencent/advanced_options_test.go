package tencent

import (
	"net/url"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/tts"
)

func TestAdvancedSignedOptions(t *testing.T) {
	off := false
	cfg := Config{Endpoint: defaultEndpoint, AppID: "123", SecretID: "id", SecretKey: "secret"}
	req := tts.Request{
		Voice:   "200000400",
		Options: tts.Options{Provider: Options{FastVoiceType: "clone-fixture", EnableLatex: &off}},
	}
	voice, err := requestVoiceQuery(req)
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := signedEndpoint(cfg, req, "mp3", "session", voice, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("FastVoiceType") != "clone-fixture" || q.Get("EnableLatex") != "false" || q.Get("Signature") == "" {
		t.Fatal("missing signed options")
	}
	req.Voice = "101001"
	if _, err := requestVoiceQuery(req); err == nil {
		t.Fatal("clone ID accepted for ordinary voice")
	}
	req.Voice = "200000000"
	req.Options = tts.Options{}
	if _, err := requestVoiceQuery(req); err == nil {
		t.Fatal("clone VoiceType accepted without clone ID")
	}
}
