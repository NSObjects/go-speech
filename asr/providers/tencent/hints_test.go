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
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/gorilla/websocket"
)

func TestTencentEncodeHints(t *testing.T) {
	for _, tc := range []struct {
		name  string
		hints []string
		want  string
		bad   bool
	}{
		{name: "unset", want: ""},
		{name: "one", hints: []string{"悟空"}, want: "悟空|5"},
		{name: "multiple and duplicate", hints: []string{"悟空", "ASR", "悟空"}, want: "悟空|5,ASR|5"},
		{name: "ten Chinese", hints: []string{"一二三四五六七八九十"}, want: "一二三四五六七八九十|5"},
		{name: "eleven Chinese", hints: []string{"一二三四五六七八九十一"}, bad: true},
		{name: "long ASCII", hints: []string{strings.Repeat("x", 31)}, bad: true},
		{name: "too many", hints: make([]string, 129), bad: true},
		{name: "empty", hints: []string{""}, bad: true},
		{name: "space", hints: []string{"Harry Potter"}, bad: true},
		{name: "tab", hints: []string{"a\tb"}, bad: true},
		{name: "separator", hints: []string{"a,b"}, bad: true},
		{name: "weight injection", hints: []string{"a|100"}, bad: true},
		{name: "bad utf8", hints: []string{string([]byte{0xff})}, bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := encodeHints(tc.hints)
			if tc.bad {
				if !speecherr.IsCode(err, speecherr.CodeInvalidArgument) {
					t.Fatalf("expected typed InvalidArgument, got %q / %v", got, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("encodeHints: got %q / %v, want %q", got, err, tc.want)
			}
		})
	}
}

func TestTencentSignedRequestHintsIsolation(t *testing.T) {
	type seenRequest struct {
		hints string
		err   error
	}
	observed := make(chan seenRequest, 2)
	client := testClient(t, func(ws *websocket.Conn, req *http.Request) {
		id := req.URL.Query().Get("voice_id")
		fail := func(err error) {
			observed <- seenRequest{err: err}
		}
		if !verifySignature(req, "secret-key") {
			fail(errors.New("Tencent hotword_list excluded from HMAC"))
			return
		}
		if req.URL.Query().Has("hotword_id") {
			fail(errors.New("unexpected persistent hotword_id"))
			return
		}
		hints := req.URL.Query().Get("hotword_list")
		if err := sendResponse(ws, id, 0, 0, nil); err != nil {
			fail(err)
			return
		}
		typ, _, err := ws.ReadMessage()
		if err != nil || typ != websocket.BinaryMessage {
			fail(errors.New("missing PCM input"))
			return
		}
		typ, data, err := ws.ReadMessage()
		if err != nil || typ != websocket.TextMessage || string(data) != `{"type":"end"}` {
			fail(errors.New("missing Tencent end message"))
			return
		}
		if err := sendResponse(ws, id, 0, 1, nil); err != nil {
			fail(err)
			return
		}
		observed <- seenRequest{hints: hints}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, tc := range []struct {
		hints []string
		want  string
	}{
		{hints: []string{"悟空", "ASR"}, want: "悟空|5,ASR|5"},
		{hints: []string{"小新"}, want: "小新|5"},
	} {
		cfg := pcmConfig()
		cfg.Hints = tc.hints
		s, err := client.OpenStream(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Send(ctx, []byte{0, 0}); err != nil {
			t.Fatal(err)
		}
		if err := s.CloseSend(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Recv(ctx); !errors.Is(err, io.EOF) {
			t.Fatalf("expected completed Tencent session, got %v", err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-observed:
			if got.err != nil || got.hints != tc.want {
				t.Fatalf("signed hints = %q / %v, want %q", got.hints, got.err, tc.want)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}

	u, err := url.Parse(client.signedURL(time.Unix(1745932688, 0), 123, "voice-id"))
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Has("hotword_list") {
		t.Fatal("per-request hotwords leaked into client configuration")
	}
}

func TestTencentRequestHintsRejectAmbiguousHotwordID(t *testing.T) {
	client, err := New(Config{
		AppID: "1250000000", SecretID: "secret-id", SecretKey: "secret-key",
		HotwordID: "managed-hotword-id",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := pcmConfig()
	req.Hints = []string{"悟空"}
	if _, err := client.OpenStream(context.Background(), req); !speecherr.IsCode(err, speecherr.CodeInvalidArgument) {
		t.Fatalf("expected conflict before WebSocket connection, got %v", err)
	}
}
