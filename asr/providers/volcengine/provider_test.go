package volcengine

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/gorilla/websocket"
)

func testRequestFrame(data []byte) (byte, byte, []byte, error) {
	if len(data) < 8 || data[0] != 0x11 {
		return 0, 0, nil, errors.New("invalid request header")
	}
	payloadSize := int(binary.BigEndian.Uint32(data[4:8]))
	if len(data)-8 != payloadSize {
		return 0, 0, nil, errors.New("invalid request size")
	}
	// Use the same parser with an expected server response type solely for
	// checking the gzip/body framing; request validation is separate below.
	copied := append([]byte(nil), data...)
	copied[1] = messageFullResponse << 4
	msg, err := parseFrame(copied)
	if err != nil {
		return 0, 0, nil, err
	}
	return data[1] >> 4, data[1] & 0xf, msg.payload, nil
}

func mockEndpoint(t *testing.T, serve func(*websocket.Conn, *http.Request)) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		serve(conn, r)
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v3/sauc/bigmodel_async"
}

func serverResult(t *testing.T, ws *websocket.Conn, last bool, result any) {
	t.Helper()
	payload, err := json.Marshal(result)
	if err != nil {
		t.Error(err)
		return
	}
	flags := byte(0)
	if last {
		flags = flagLast
	}
	frame, err := encodeFrame(messageFullResponse, flags, 1, payload)
	if err != nil {
		t.Error(err)
		return
	}
	if err := ws.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		t.Error(err)
	}
}

func TestWebSocketStreamRoundTrip(t *testing.T) {
	observed := make(chan error, 1)
	endpoint := mockEndpoint(t, func(ws *websocket.Conn, req *http.Request) {
		report := func(err error) { observed <- err }
		if req.Header.Get("X-Api-Key") != "test-key" ||
			req.Header.Get("X-Api-Resource-Id") != ResourceSeedASR2 ||
			req.Header.Get("X-Api-Request-Id") == "" ||
			req.Header.Get("X-Api-Sequence") != "-1" {
			report(fmt.Errorf("invalid authentication headers"))
			return
		}
		typ, msg, err := ws.ReadMessage()
		if err != nil || typ != websocket.BinaryMessage {
			report(fmt.Errorf("initial binary frame: %v", err))
			return
		}
		kind, _, payload, err := testRequestFrame(msg)
		if err != nil || kind != messageFullRequest {
			report(fmt.Errorf("initial request: %v, kind %d", err, kind))
			return
		}
		var config struct {
			Audio struct {
				Format string `json:"format"`
				Codec  string `json:"codec"`
				Rate   int    `json:"rate"`
			} `json:"audio"`
			Request struct {
				ModelName string `json:"model_name"`
				Corpus    struct {
					Context string `json:"context"`
				} `json:"corpus"`
			} `json:"request"`
		}
		if err := json.Unmarshal(payload, &config); err != nil {
			report(err)
			return
		}
		if config.Audio.Format != "ogg" || config.Audio.Codec != "opus" ||
			config.Audio.Rate != 16000 || config.Request.ModelName != "bigmodel" ||
			!strings.Contains(config.Request.Corpus.Context, "go-speech") {
			report(fmt.Errorf("incorrect ASR config: %+v", config))
			return
		}
		for i := 0; i < 3; i++ {
			typ, frame, err := ws.ReadMessage()
			if err != nil || typ != websocket.BinaryMessage {
				report(fmt.Errorf("audio frame %d: %v", i, err))
				return
			}
			kind, flags, audioBytes, err := testRequestFrame(frame)
			if err != nil || kind != messageAudioRequest {
				report(fmt.Errorf("audio request %d: %v", i, err))
				return
			}
			if i < 2 && (flags != 0 || !bytes.Equal(audioBytes, []byte{byte(i), 0xff})) {
				report(fmt.Errorf("audio was altered or early-ended: %v %x", flags, audioBytes))
				return
			}
			if i == 2 && (flags != flagLast || len(audioBytes) != 0) {
				report(fmt.Errorf("missing final audio marker: %x %x", flags, audioBytes))
				return
			}
		}
		serverResult(t, ws, false, map[string]any{
			"code": 1000, "result": map[string]any{
				"text": "hello", "utterances": []any{map[string]any{"text": "hello", "definite": false}},
			},
		})
		serverResult(t, ws, true, map[string]any{
			"code": 1000, "result": map[string]any{
				"text": "hello world", "utterances": []any{map[string]any{
					"text": "hello world", "definite": true, "start_time": 0, "end_time": 1000,
				}},
			},
		})
		report(nil)
	})
	client, err := New(Config{APIKey: "test-key", Endpoint: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	registry := asr.NewRegistry()
	if err := registry.Register("doubao", asr.Provider{Stream: client}); err != nil {
		t.Fatal(err)
	}
	provider, ok := registry.Lookup("doubao")
	if !ok {
		t.Fatal("registered ASR provider missing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := provider.Stream.OpenStream(ctx, asr.Config{
		Format: audio.Format{Encoding: audio.EncodingOpus, SampleRate: 16000, Channels: 1},
		Hints:  []string{"go-speech"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for i := 0; i < 2; i++ {
		if err := session.Send(ctx, []byte{byte(i), 0xff}); err != nil {
			t.Fatalf("send audio: %v", err)
		}
	}
	if err := session.CloseSend(ctx); err != nil {
		t.Fatal(err)
	}
	if err := session.CloseSend(ctx); err != nil {
		t.Fatalf("CloseSend should be idempotent: %v", err)
	}
	if err := session.Send(ctx, []byte{0}); !errors.Is(err, asr.ErrInputClosed) {
		t.Fatalf("input closed: %v", err)
	}
	for i, want := range []asr.Event{
		{Type: asr.EventPartial, Text: "hello"},
		{Type: asr.EventFinal, Text: "hello world"},
	} {
		got, err := session.Recv(ctx)
		if err != nil || got != want {
			t.Fatalf("event %d: got %#v err %v want %#v", i, got, err, want)
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := session.Recv(ctx); !errors.Is(err, io.EOF) {
			t.Fatalf("Recv %d after final: %v", i, err)
		}
	}
	if err := <-observed; err != nil {
		t.Fatal(err)
	}
}

func TestProtocolErrorsAndTerminalReason(t *testing.T) {
	cases := []struct {
		name  string
		frame func() []byte
		want  speecherr.Code
	}{
		{"invalid gzip", func() []byte { return []byte{0x11, 0x90, 0x11, 0, 0, 0, 0, 1, 42} }, speecherr.CodeProtocol},
		{"remote failure", func() []byte {
			payload := []byte(`{"message":"bad auth"}`)
			frame := make([]byte, 12+len(payload))
			copy(frame[:4], []byte{0x11, 0xf0, 0x10, 0})
			binary.BigEndian.PutUint32(frame[4:8], 403)
			binary.BigEndian.PutUint32(frame[8:12], uint32(len(payload)))
			copy(frame[12:], payload)
			return frame
		}, speecherr.CodeUnauthorized},
		{"invalid frame", func() []byte { return []byte("not a frame") }, speecherr.CodeProtocol},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := mockEndpoint(t, func(ws *websocket.Conn, _ *http.Request) {
				_, _, _ = ws.ReadMessage()
				_ = ws.WriteMessage(websocket.BinaryMessage, tc.frame())
				_, _, _ = ws.ReadMessage()
			})
			c, err := New(Config{APIKey: "x", Endpoint: endpoint})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			s, err := c.OpenStream(ctx, asr.Config{
				Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			_, first := s.Recv(ctx)
			if !speecherr.IsCode(first, tc.want) {
				t.Fatalf("first error %v, want %s", first, tc.want)
			}
			_, next := s.Recv(context.Background())
			if next != first {
				t.Fatalf("terminal failure changed: first %v next %v", first, next)
			}
		})
	}
}

func TestContextCancellationAndExplicitClose(t *testing.T) {
	endpoint := mockEndpoint(t, func(ws *websocket.Conn, _ *http.Request) {
		_, _, _ = ws.ReadMessage()
		_, _, _ = ws.ReadMessage()
	})
	client, _ := New(Config{APIKey: "x", Endpoint: endpoint})
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := client.OpenStream(ctx, asr.Config{
		Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := stream.Recv(context.Background())
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("parent cancellation lost: %v", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("Recv did not unblock on parent cancellation")
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(context.Background(), []byte{0, 0}); !errors.Is(err, context.Canceled) {
		t.Fatalf("closed cancelled stream: %v", err)
	}
}

func TestConfigAndAudioValidation(t *testing.T) {
	for _, cfg := range []Config{
		{}, {APIKey: "x", Endpoint: "ws://not-loopback/api/v3/sauc/bigmodel_async"},
		{APIKey: "x", Endpoint: "https://x.example/api/v3/sauc/bigmodel_async"},
		{APIKey: "x", Endpoint: "wss://x.example/wrong"},
		{APIKey: "x", StartTimeout: -1},
	} {
		if _, err := New(cfg); !speecherr.IsCode(err, speecherr.CodeInvalidArgument) {
			t.Fatalf("invalid provider config %+v: %v", cfg, err)
		}
	}
	for _, f := range []audio.Format{
		{Encoding: audio.EncodingOpus, SampleRate: 48000, Channels: 1},
		{Encoding: audio.EncodingAAC, SampleRate: 16000, Channels: 1},
		{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 3},
	} {
		if _, _, err := modelAudio(f); !speecherr.IsCode(err, speecherr.CodeUnsupported) {
			t.Fatalf("accepted unsupported format %+v: %v", f, err)
		}
	}
	for enc, want := range map[audio.Encoding]string{
		audio.EncodingPCM16LE: "pcm", audio.EncodingWAV: "wav",
		audio.EncodingMP3: "mp3", audio.EncodingOpus: "ogg",
	} {
		got, _, err := modelAudio(audio.Format{Encoding: enc, SampleRate: 16000, Channels: 1})
		if err != nil || got != want {
			t.Fatalf("encoding %s maps to %q: %v", enc, got, err)
		}
	}
}

func TestSequencedLastResponseAndLimits(t *testing.T) {
	jsonBody := []byte(`{"code":1000,"result":{"text":"finished"}}`)
	frame, err := encodeFrame(messageFullResponse, 3, 1, jsonBody)
	if err != nil {
		t.Fatal(err)
	}
	// Volcengine response with flags=3 has a signed 4-byte sequence
	// between the header and payload length.
	extended := make([]byte, 4+4+len(frame)-4)
	copy(extended, frame[:4])
	binary.BigEndian.PutUint32(extended[4:8], ^uint32(6))
	copy(extended[8:], frame[4:])
	msg, err := parseFrame(extended)
	if err != nil {
		t.Fatal(err)
	}
	if msg.seq != -7 || msg.flags != 3 {
		t.Fatalf("invalid final sequence %+v", msg)
	}
	results, last, err := msg.result()
	if err != nil || !last || len(results) != 1 || results[0].Text != "finished" {
		t.Fatalf("unexpected parsed result: %+v last=%v err=%v", results, last, err)
	}
	if _, err := encodeFrame(messageAudioRequest, 0, 0, make([]byte, maxWirePayload+1)); err == nil {
		t.Fatal("oversized input must be rejected before compression")
	}
}
