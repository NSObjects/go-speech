package tencent

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/speecherr"
)

func TestTencentSegmentationValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		engine string
		needVAD bool
		silence time.Duration
		maxSpeak time.Duration
		wantInvalid bool
	}{
		{name: "legacy defaults", engine: "16k_zh", needVAD: true},
		{name: "lower boundaries", engine: "16k_zh", needVAD: true, silence: 500*time.Millisecond, maxSpeak: 5*time.Second},
		{name: "upper boundaries", engine: "16k_zh", needVAD: true, silence: 2*time.Second, maxSpeak: 90*time.Second},
		{name: "nonchinese supported", engine: "16k_en_large", needVAD: true, silence: 700*time.Millisecond},
		{name: "financial supports silence", engine: "8k_zh_finance", needVAD: true, silence: 1*time.Second},
		{name: "VAD disabled", engine: "16k_zh", silence: 700*time.Millisecond, wantInvalid: true},
		{name: "too-short silence", engine: "16k_zh", needVAD: true, silence: 499*time.Millisecond, wantInvalid: true},
		{name: "too-long silence", engine: "16k_zh", needVAD: true, silence: 2001*time.Millisecond, wantInvalid: true},
		{name: "fractional silence", engine: "16k_zh", needVAD: true, silence: 700*time.Millisecond + time.Microsecond, wantInvalid: true},
		{name: "negative silence", engine: "16k_zh", needVAD: true, silence: -time.Second, wantInvalid: true},
		{name: "too-short forced split", engine: "16k_zh", needVAD: true, maxSpeak: 4999*time.Millisecond, wantInvalid: true},
		{name: "too-long forced split", engine: "16k_zh", needVAD: true, maxSpeak: 90001*time.Millisecond, wantInvalid: true},
		{name: "fractional forced split", engine: "16k_zh", needVAD: true, maxSpeak: 10*time.Second + time.Microsecond, wantInvalid: true},
		{name: "negative forced split", engine: "16k_zh", needVAD: true, maxSpeak: -time.Second, wantInvalid: true},
		{name: "V2 silence unsupported", engine: "16k_zh_en_2.0", needVAD: true, silence: 700*time.Millisecond, wantInvalid: true},
		{name: "V2 forced split unsupported", engine: "16k_zh_en_2.0", needVAD: true, maxSpeak: 10*time.Second, wantInvalid: true},
		{name: "Japanese engine silence unsupported", engine: "16k_ja", needVAD: true, silence: 700*time.Millisecond, wantInvalid: true},
		{name: "English forced split unsupported", engine: "16k_en", needVAD: true, maxSpeak: 10*time.Second, wantInvalid: true},
		{name: "finance forced split unsupported", engine: "8k_zh_finance", needVAD: true, maxSpeak: 10*time.Second, wantInvalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(Config{
				AppID: "1250000000", SecretID: "id", SecretKey: "secret",
				EngineModel: tc.engine, NeedVAD: tc.needVAD,
				VADSilenceTime: tc.silence, MaxSpeakTime: tc.maxSpeak,
			})
			if tc.wantInvalid {
				if !speecherr.IsCode(err, speecherr.CodeInvalidArgument) {
					t.Fatalf("want InvalidArgument before dial, got %v", err)
				}
			} else if err != nil {
				t.Fatalf("valid segmentation options: %v", err)
			}
		})
	}
}

func TestTencentSegmentationSignedURL(t *testing.T) {
	for _, tc := range []struct {
		name string
		silence time.Duration
		maxSpeak time.Duration
		wantSilence string
		wantMaxSpeak string
	}{
		{name: "default omits optional params"},
		{name: "custom 700ms and 10s", silence: 700*time.Millisecond, maxSpeak: 10*time.Second, wantSilence:"700", wantMaxSpeak:"10000"},
		{name: "lower bounds", silence:500*time.Millisecond, maxSpeak: 5*time.Second, wantSilence:"500", wantMaxSpeak:"5000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, err := New(Config{
				AppID:"1250000000", SecretID:"id", SecretKey:"secret",
				EngineModel:"16k_zh", NeedVAD:true,
				VADSilenceTime:tc.silence, MaxSpeakTime:tc.maxSpeak,
			})
			if err != nil {t.Fatal(err)}
			u, err := url.Parse(client.signedURL(time.Unix(1745932688,0),123,"voice-id"))
			if err != nil {t.Fatal(err)}
			q := u.Query()
			if got:=q.Get("vad_silence_time"); got!=tc.wantSilence {t.Fatalf("vad_silence_time=%q want %q",got,tc.wantSilence)}
			if got:=q.Get("max_speak_time"); got!=tc.wantMaxSpeak {t.Fatalf("max_speak_time=%q want %q",got,tc.wantMaxSpeak)}
			if q.Has("vad_silence_time") != (tc.silence != 0) || q.Has("max_speak_time") != (tc.maxSpeak != 0) {
				t.Fatal("optional segmentation parameter omitted or unexpectedly signed")
			}
			if !verifySignature(&http.Request{Host:u.Host,URL:u},"secret") {
				t.Fatal("signature did not include Tencent segmentation options")
			}
		})
	}
}
