package tencent

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/asr/internal/contracttest"
	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/gorilla/websocket"
)

func testClient(t *testing.T, handler func(*websocket.Conn, *http.Request)) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, req, nil)
		if err != nil {
			t.Errorf("WebSocket upgrade: %v", err)
			return
		}
		defer conn.Close()
		handler(conn, req)
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{
		AppID: "1250000000", SecretID: "secret-id", SecretKey: "secret-key",
		Endpoint: "ws" + strings.TrimPrefix(server.URL, "http"),
		NeedVAD:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func sendResponse(ws *websocket.Conn, id string, code, final int, result *segment) error {
	msg, err := json.Marshal(map[string]any{
		"code": code, "message": "success", "voice_id": id,
		"final": final, "result": result,
	})
	if err != nil {
		return err
	}
	return ws.WriteMessage(websocket.TextMessage, msg)
}

func verifySignature(r *http.Request, secret string) bool {
	q := r.URL.Query()
	signature := q.Get("signature")
	if signature == "" {
		return false
	}
	q.Del("signature")
	mac := hmac.New(sha1.New, []byte(secret))
	_, _ = mac.Write([]byte(r.Host + r.URL.Path + "?" + q.Encode()))
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(signature), []byte(want))
}

func pcmConfig() asr.Config {
	return asr.Config{Format: audio.Format{
		Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1,
	}}
}

func TestTencentSignedURL(t *testing.T) {
	client, err := New(Config{
		AppID: "1250000000", SecretID: "my-secret-id", SecretKey: "my-secret-key",
		EngineModel: "16k_zh_en_2.0", NeedVAD: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1745932688, 0)
	u, err := url.Parse(client.signedURL(now, 8743357, "voice-id"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Scheme != "wss" || u.Host != "asr.cloud.tencent.com" ||
		u.Path != "/asr/v2/1250000000" ||
		q.Get("secretid") != "my-secret-id" ||
		q.Get("timestamp") != "1745932688" ||
		q.Get("expired") != "1745936288" ||
		q.Get("voice_format") != "1" ||
		q.Get("needvad") != "1" ||
		q.Get("engine_model_type") != "16k_zh_en_2.0" ||
		q.Get("nonce") != "8743357" ||
		q.Get("voice_id") != "voice-id" {
		t.Fatalf("incorrect Tencent signed parameters: %v", q)
	}
	request := &http.Request{Host: u.Host, URL: u}
	if !verifySignature(request, "my-secret-key") {
		t.Fatal("HMAC-SHA1 signature does not match canonical query")
	}
	if verifySignature(request, "wrong-key") {
		t.Fatal("signature must be bound to the configured secret")
	}
	q.Set("signature", "tampered")
	u.RawQuery = q.Encode()
	if verifySignature(&http.Request{Host: u.Host, URL: u}, "my-secret-key") {
		t.Fatal("tampered signature accepted")
	}
}

func TestTencentStreamResultsAndEOF(t *testing.T) {
	saw := make(chan error, 1)
	client := testClient(t, func(ws *websocket.Conn, req *http.Request) {
		if !verifySignature(req, "secret-key") ||
			req.URL.Path != "/asr/v2/1250000000" ||
			req.URL.Query().Get("needvad") != "1" ||
			req.URL.Query().Get("voice_format") != "1" {
			saw <- errors.New("invalid signed WebSocket handshake")
			return
		}
		id := req.URL.Query().Get("voice_id")
		if err := sendResponse(ws, id, 0, 0, nil); err != nil {
			saw <- err
			return
		}
		typ, payload, err := ws.ReadMessage()
		if err != nil || typ != websocket.BinaryMessage || string(payload) != string([]byte{1, 0, 2, 0}) {
			saw <- errors.New("invalid PCM payload")
			return
		}
		if err := sendResponse(ws, id, 0, 0, &segment{SliceType: 1, Index: 0, Text: "你好"}); err != nil {
			saw <- err
			return
		}
		if err := sendResponse(ws, id, 0, 0, &segment{SliceType: 2, Index: 0, Text: "你好世界"}); err != nil {
			saw <- err
			return
		}
		if err := sendResponse(ws, id, 0, 0, &segment{SliceType: 2, Index: 0, Text: "你好世界"}); err != nil {
			saw <- err
			return
		}
		typ, payload, err = ws.ReadMessage()
		if err != nil || typ != websocket.TextMessage || string(payload) != `{"type":"end"}` {
			saw <- errors.New("CloseSend did not send Tencent end message")
			return
		}
		if err := sendResponse(ws, id, 0, 1, nil); err != nil {
			saw <- err
			return
		}
		saw <- nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.OpenStream(ctx, pcmConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err := stream.Send(ctx, []byte{1, 0, 2, 0}); err != nil {
		t.Fatal(err)
	}
	if err := stream.CloseSend(ctx); err != nil {
		t.Fatal(err)
	}
	if err := stream.CloseSend(ctx); err != nil {
		t.Fatalf("CloseSend must be idempotent: %v", err)
	}
	if err := stream.Send(ctx, []byte{3, 0}); !errors.Is(err, asr.ErrInputClosed) {
		t.Fatalf("Send after CloseSend: %v", err)
	}
	for i, expected := range []asr.Event{
		{Type: asr.EventPartial, Text: "你好"},
		{Type: asr.EventFinal, Text: "你好世界"},
	} {
		event, err := stream.Recv(ctx)
		if err != nil || event != expected {
			t.Fatalf("recognition event %d: got %+v, %v; want %+v", i, event, err, expected)
		}
	}
	for i := 0; i < 3; i++ {
		_, err := stream.Recv(context.Background())
		if !errors.Is(err, io.EOF) {
			t.Fatalf("Recv after final %d: %v", i, err)
		}
	}
	if err := <-saw; err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTencentSharedASRStreamContract(t *testing.T) {
	contracttest.RunStreamLifecycle(t, func(t *testing.T, parent context.Context) asr.Stream {
		client := testClient(t, func(ws *websocket.Conn, req *http.Request) {
			id := req.URL.Query().Get("voice_id")
			if err := sendResponse(ws, id, 0, 0, nil); err != nil {
				return
			}
			for {
				if _, _, err := ws.ReadMessage(); err != nil {
					return
				}
			}
		})
		stream, err := client.OpenStream(parent, pcmConfig())
		if err != nil {
			t.Fatal(err)
		}
		return stream
	})
}

func TestTencentRemoteErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		want speecherr.Code
	}{
		{"auth", 4002, speecherr.CodeUnauthorized},
		{"quota", 4006, speecherr.CodeRateLimited},
		{"decode", 4007, speecherr.CodeInvalidArgument},
		{"idle timeout", 4008, speecherr.CodeTimeout},
		{"server overload", 5000, speecherr.CodeUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := testClient(t, func(ws *websocket.Conn, req *http.Request) {
				id := req.URL.Query().Get("voice_id")
				_ = sendResponse(ws, id, tc.code, 0, nil)
			})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err := client.OpenStream(ctx, pcmConfig())
			if !speecherr.IsCode(err, tc.want) {
				t.Fatalf("response code %d: expected %s, got %v", tc.code, tc.want, err)
			}
		})
	}
}

func TestTencentMalformedHandshake(t *testing.T) {
	client := testClient(t, func(ws *websocket.Conn, req *http.Request) {
		_ = sendResponse(ws, req.URL.Query().Get("voice_id")+"wrong", 0, 0, nil)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := client.OpenStream(ctx, pcmConfig())
	if !speecherr.IsCode(err, speecherr.CodeProtocol) {
		t.Fatalf("mismatched voice_id must fail: %v", err)
	}
}

func TestTencentValidationAndAudioCapabilities(t *testing.T) {
	for _, cfg := range []Config{
		{}, {AppID: "not-a-number", SecretID: "x", SecretKey: "y"},
		{AppID: "1", SecretID: "x", SecretKey: "y", Endpoint: "ws://example.com"},
		{AppID: "1", SecretID: "x", SecretKey: "y", Endpoint: "wss://asr.cloud.tencent.com/asr/v2/1"},
		{AppID: "1", SecretID: "x", SecretKey: "y", StartTimeout: -time.Second},
	} {
		if _, err := New(cfg); !speecherr.IsCode(err, speecherr.CodeInvalidArgument) {
			t.Fatalf("invalid config %+v: %v", cfg, err)
		}
	}
	for _, tc := range []struct {
		engine string
		rate   int
	}{
		{"16k_zh", 16000}, {"16k_zh_en_2.0", 16000}, {"8k_zh", 8000},
	} {
		if err := SupportedAudio(tc.engine).Validate(audio.Format{
			Encoding: audio.EncodingPCM16LE, SampleRate: tc.rate, Channels: 1,
		}); err != nil {
			t.Fatalf("valid %s PCM format: %v", tc.engine, err)
		}
	}
	client, err := New(Config{AppID: "1", SecretID: "x", SecretKey: "y"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		config   asr.Config
		want     speecherr.Code
		sentinel error
	}{
		{"invalid audio", asr.Config{Format: audio.Format{
			Encoding: audio.EncodingPCM16LE, SampleRate: 0, Channels: 1,
		}}, speecherr.CodeInvalidArgument, audio.ErrInvalidFormat},
		{"Ogg Opus is not Tencent proprietary Opus", asr.Config{Format: audio.Format{
			Encoding: audio.EncodingOpus, SampleRate: 16000, Channels: 1,
		}}, speecherr.CodeUnsupported, audio.ErrUnsupportedFormat},
		{"unsupported rate", asr.Config{Format: audio.Format{
			Encoding: audio.EncodingPCM16LE, SampleRate: 8000, Channels: 1,
		}}, speecherr.CodeUnsupported, audio.ErrUnsupportedFormat},
		{"stereo", asr.Config{Format: audio.Format{
			Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 2,
		}}, speecherr.CodeUnsupported, audio.ErrUnsupportedFormat},
		{"language", asr.Config{Format: pcmConfig().Format, Language: "en-US"}, speecherr.CodeUnsupported, nil},
		{"hints", asr.Config{Format: pcmConfig().Format, Hints: []string{"hello"}}, speecherr.CodeUnsupported, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.OpenStream(context.Background(), tc.config)
			if !speecherr.IsCode(err, tc.want) {
				t.Fatalf("expected %s, got %v", tc.want, err)
			}
			if tc.sentinel != nil && !errors.Is(err, tc.sentinel) {
				t.Fatalf("expected errors.Is(%v), got %v", tc.sentinel, err)
			}
		})
	}
}

func TestTencentIndependentVoiceIDs(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]bool{}
	client := testClient(t, func(ws *websocket.Conn, req *http.Request) {
		id := req.URL.Query().Get("voice_id")
		mu.Lock()
		if seen[id] {
			t.Errorf("reused Tencent voice_id: %q", id)
		}
		seen[id] = true
		mu.Unlock()
		_ = sendResponse(ws, id, 0, 0, nil)
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	})
	for i := 0; i < 3; i++ {
		ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
		s, err := client.OpenStream(ctx, pcmConfig())
		if err != nil {
			stop()
			t.Fatal(err)
		}
		_ = s.Close()
		stop()
	}
	if len(seen) != 3 {
		t.Fatalf("unique voice IDs: got %d", len(seen))
	}
}
