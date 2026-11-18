package tencent

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/gorilla/websocket"
)

func TestTencentOptionalPCMInputRateAndHotwordID(t *testing.T) {
	const hotwordID = "da3f5f5555cf11eda6da525400aec391"
	serverErr := make(chan error, 1)
	client := testClient(t, func(ws *websocket.Conn, req *http.Request) {
		if got := req.URL.Query().Get("input_sample_rate"); got != "8000" {
			serverErr <- errors.New("missing input_sample_rate=8000")
			return
		}
		if got := req.URL.Query().Get("hotword_id"); got != hotwordID {
			serverErr <- errors.New("missing Tencent hotword_id")
			return
		}
		if !verifySignature(req, "secret-key") {
			serverErr <- errors.New("signature does not include optional Tencent parameters")
			return
		}
		if err := sendResponse(ws, req.URL.Query().Get("voice_id"), 0, 0, nil); err != nil {
			serverErr <- err
			return
		}
		typ, data, err := ws.ReadMessage()
		if err != nil || typ != websocket.BinaryMessage || len(data) != 3200 {
			serverErr <- errors.New("expected 100ms of unchanged 8k PCM16LE")
			return
		}
		typ, data, err = ws.ReadMessage()
		if err != nil || typ != websocket.TextMessage || string(data) != `{"type":"end"}` {
			serverErr <- errors.New("expected upload end message")
			return
		}
		if err := sendResponse(ws, req.URL.Query().Get("voice_id"), 0, 1, nil); err != nil {
			serverErr <- err
			return
		}
		serverErr <- nil
	})
	client.cfg.InputSampleRate = 8000
	client.cfg.HotwordID = hotwordID

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.OpenStream(ctx, asr.Config{Format: audio.Format{
		Encoding: audio.EncodingPCM16LE, SampleRate: 8000, Channels: 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err := stream.Send(ctx, make([]byte, 3200)); err != nil {
		t.Fatal(err)
	}
	if err := stream.CloseSend(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("expected stable EOF after Tencent final, got %v", err)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}

	_, err = client.OpenStream(ctx, pcmConfig())
	if !speecherr.IsCode(err, speecherr.CodeUnsupported) {
		t.Fatalf("8k input_sample_rate must reject unconverted 16k PCM: %v", err)
	}
}

func TestTencentInputRateAndHotwordValidation(t *testing.T) {
	base := Config{AppID: "1250000000", SecretID: "secret-id", SecretKey: "secret-key", EngineModel: "16k_zh"}
	cases := []struct {
		name string
		rate int
		model string
		hotword string
	}{
		{"reject16kOverride", 16000, "16k_zh", ""},
		{"rejectNegative", -8000, "16k_zh", ""},
		{"rejectOverrideOn8kEngine", 8000, "8k_zh", ""},
		{"rejectEmbeddedQuery", 0, "16k_zh", "abc&needvad=1"},
		{"rejectWhitespace", 0, "16k_zh", "a b"},
		{"rejectNewline", 0, "16k_zh", "a\nb"},
		{"rejectLongIdentifier", 0, "16k_zh", strings.Repeat("x", 129)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			cfg.InputSampleRate, cfg.EngineModel, cfg.HotwordID = tc.rate, tc.model, tc.hotword
			if _, err := New(cfg); !speecherr.IsCode(err, speecherr.CodeInvalidArgument) {
				t.Fatalf("expected invalid argument, got %v", err)
			}
		})
	}
	cfg := base
	cfg.InputSampleRate = 8000
	cfg.HotwordID = "valid-hotword-id"
	client, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(client.signedURL(time.Unix(1745932688, 0), 123, "voice-id"))
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("input_sample_rate") != "8000" || u.Query().Get("hotword_id") != cfg.HotwordID {
		t.Fatalf("missing signed optional parameters: %s", u.RawQuery)
	}
	if !verifySignature(&http.Request{Host: u.Host, URL: u}, cfg.SecretKey) {
		t.Fatal("invalid optional-parameter signature")
	}
	defaultClient, err := New(base)
	if err != nil {
		t.Fatal(err)
	}
	defaultURL, _ := url.Parse(defaultClient.signedURL(time.Unix(1745932688, 0), 123, "voice-id"))
	if _, exists := defaultURL.Query()["input_sample_rate"]; exists {
		t.Fatal("default Tencent PCM should not override sample rate")
	}
	if _, exists := defaultURL.Query()["hotword_id"]; exists {
		t.Fatal("default Tencent account hotwords should not be overridden")
	}
}
