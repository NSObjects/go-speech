package tencent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/internal/tc3"
	"github.com/NSObjects/go-speech/speecherr"
)

func TestSentenceHTTPWire(t *testing.T) {
	n := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		ts, _ := strconv.ParseInt(r.Header.Get("X-TC-Timestamp"), 10, 64)
		if r.Method != http.MethodPost || r.Header.Get("X-TC-Action") != "SentenceRecognition" ||
			r.Header.Get("X-TC-Version") != "2019-06-14" || r.Header.Get("X-TC-Token") != "token" ||
			r.Header.Get("Authorization") != tc3.Authorization("id", "key", r.Host, "SentenceRecognition", "asr", time.Unix(ts, 0), body) {
			t.Error("invalid signed request")
		}
		var p sentenceRequest
		if err := json.Unmarshal(body, &p); err != nil {
			t.Error(err)
			return
		}
		if p.Engine != "16k_zh" || p.WordInfo == nil || *p.WordInfo != 2 || p.HotwordList != "腾讯云|11" || p.HotwordID != "dict" {
			t.Errorf("unexpected options: %+v", p)
		}
		if n == 1 && (p.SourceType != 1 || p.DataLen != 4 || len(p.Data) != 4 || p.URL != "" || p.VoiceFormat != SentencePCM) {
			t.Error("invalid inline audio")
		}
		if n == 2 && (p.SourceType != 0 || p.URL != "https://example.com/audio.m4a?sig=abc" || p.DataLen != 0 || p.Data != nil || p.VoiceFormat != SentenceM4A) {
			t.Error("invalid URL request")
		}
		_, _ = w.Write([]byte(`{"Response":{"RequestId":"trace","Result":"你好","AudioDuration":100,"WordSize":1,"WordList":[{"Word":"你好","StartTime":0,"EndTime":90}]}}`))
	}))
	defer server.Close()
	c, err := NewHTTP(HTTPConfig{SecretID: "id", SecretKey: "key", Token: "token", Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	two := 2
	cfg := asr.Config{Options: asr.Options{Provider: HTTPOptions{WordInfo: &two, HotwordID: "dict", Hotwords: []Hotword{{Word: "腾讯云", Weight: 11}}}}}
	res, err := c.Recognize(context.Background(), audio.Buffer{Data: []byte{1, 2, 3, 4}, Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1}}, cfg)
	if err != nil || res.Text != "你好" || len(res.Utterances) != 1 || len(res.Utterances[0].Words) != 1 {
		t.Fatalf("inline result: %v", err)
	}
	d, ok := res.Provider.(SentenceDetails)
	if !ok || d.RequestID != "trace" || d.AudioDuration != 100*time.Millisecond {
		t.Fatal("missing native metadata")
	}
	if _, err := c.RecognizeURL(context.Background(), "https://example.com/audio.m4a?sig=abc", SentenceM4A, cfg); err != nil {
		t.Fatal(err)
	}
	if n != 2 || c.Capabilities().Streaming {
		t.Fatal("unexpected request count or capability")
	}
}

func TestSentenceHTTPErrors(t *testing.T) {
	for _, tc := range []struct {
		code string
		want speecherr.Code
	}{
		{"FailedOperation.UserHasNoAmount", speecherr.CodeRateLimited},
		{"FailedOperation.UserNotRegistered", speecherr.CodeUnauthorized},
		{"InvalidParameterValue.ErrorVoicedataTooLong", speecherr.CodeInvalidArgument},
		{"InternalError.ErrorRecognize", speecherr.CodeInternal},
	} {
		raw := []byte(`{"Response":{"RequestId":"trace","Error":{"Code":"` + tc.code + `","Message":"private"}}}`)
		_, err := decodeSentence(raw, 200)
		if !speecherr.IsCode(err, tc.want) || strings.Contains(err.Error(), "private") {
			t.Fatalf("classification: %v", err)
		}
	}
	r, err := decodeSentence([]byte(`{"Response":{"RequestId":"trace","Result":"","AudioDuration":0,"WordSize":null,"WordList":null}}`), 200)
	if err != nil || r.Provider.(SentenceDetails).WordSize != nil || r.Utterances != nil {
		t.Fatalf("nullable metadata: %v", err)
	}
	for _, raw := range []string{
		`{"Response":{"RequestId":"trace","AudioDuration":1}}`,
		`{"Response":{"RequestId":"trace","Result":"a","AudioDuration":1,"WordSize":2,"WordList":[]}}`,
		`{"Response":{"RequestId":"trace","Result":"a","AudioDuration":1,"WordList":[{"Word":"a","StartTime":2,"EndTime":1}]}}`,
	} {
		if _, err := decodeSentence([]byte(raw), 200); !speecherr.IsCode(err, speecherr.CodeProtocol) {
			t.Fatalf("malformed result: %v", err)
		}
	}
}

func TestSentenceHTTPValidation(t *testing.T) {
	c, err := NewHTTP(HTTPConfig{SecretID: "id", SecretKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, size := range []int{0, 3, 1920002, 2250001} {
		if _, err := c.RecognizeBytes(ctx, make([]byte, size), SentencePCM, asr.Config{}); !speecherr.IsCode(err, speecherr.CodeInvalidArgument) {
			t.Fatalf("invalid PCM size %d: %v", size, err)
		}
	}
	three := 3
	if _, err := c.prepareSentence(SentencePCM, asr.Config{Options: asr.Options{Provider: HTTPOptions{ConvertNumMode: &three}}}); err == nil {
		t.Fatal("stream-only conversion mode accepted")
	}
	if _, err := c.prepareSentence(SentencePCM, asr.Config{Options: asr.Options{Provider: Options{}}}); err == nil {
		t.Fatal("stream options accepted")
	}
}
