package tencent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/tts"
)

func TestHTTPTextToVoice(t *testing.T) {
	wav, err := audio.EncodeWAVPCM16(audio.Buffer{Data: []byte{1, 2}, Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1}})
	if err != nil {
		t.Fatal(err)
	}
	const input = `<speak>你好<break time="500ms"/>世界</speak>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p httpRequest
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if r.Header.Get("X-TC-Action") != "TextToVoice" || r.Header.Get("X-TC-Version") != "2019-08-23" ||
			!strings.Contains(r.Header.Get("Authorization"), "/tts/tc3_request") || p.Text != input ||
			p.VoiceType != 101001 || p.Codec != "wav" || p.SessionID == "" || p.ProjectID == nil || *p.ProjectID != 0 ||
			p.EnableSubtitle == nil || !*p.EnableSubtitle {
			t.Error("invalid native request")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Response": map[string]any{
			"RequestId": "trace", "SessionId": p.SessionID, "Audio": wav.Data,
			"Subtitles": []map[string]any{{"Text": "你", "BeginTime": 0, "EndTime": 100, "BeginIndex": 0, "EndIndex": 1, "Phoneme": nil}},
		}})
	}))
	defer server.Close()
	c, err := NewHTTP(HTTPConfig{SecretID: "id", SecretKey: "key", Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	zero, yes := int64(0), true
	req := tts.Request{Text: input, Voice: "101001", TextFormat: tts.TextSSML, Options: tts.Options{Provider: HTTPOptions{Options: Options{EnableSubtitle: &yes}, ProjectID: &zero}}}
	if _, err := c.Synthesize(context.Background(), req); err == nil {
		t.Fatal("audio-only synthesis accepted subtitles")
	}
	got, err := c.SynthesizeDetailed(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if got.RequestID != "trace" || got.Audio.Format.Encoding != audio.EncodingWAV || len(got.Alignments) != 1 ||
		len(got.Subtitles) != 1 || got.Subtitles[0].Phoneme != nil || got.Subtitles[0].BeginIndex == nil {
		t.Fatal("native result lost")
	}
	if caps := c.Capabilities(); !caps.Batch || caps.Streaming || caps.Duplex {
		t.Fatal("incorrect HTTP capabilities")
	}
}

func TestHTTPRejectInvalidResponse(t *testing.T) {
	f := audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1}
	for _, raw := range []string{
		`{"Response":{"Audio":"AQI=","SessionId":"wrong","RequestId":"trace"}}`,
		`{"Response":{"Audio":"AQ==","SessionId":"session","RequestId":"trace"}}`,
		`{"Response":{"Audio":"not base64","SessionId":"session","RequestId":"trace"}}`,
		`{"Response":{"Audio":"AQI=","SessionId":"session","RequestId":"trace","Subtitles":[{"BeginTime":20,"EndTime":10}]}}`,
	} {
		if _, err := decodeHTTPResult([]byte(raw), 200, "session", f); err == nil {
			t.Fatal("malformed response accepted")
		}
	}
}
