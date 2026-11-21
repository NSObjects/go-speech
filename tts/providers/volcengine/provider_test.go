package volcengine

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/NSObjects/go-speech/tts"
	"github.com/gorilla/websocket"
)

func mockTTS(t *testing.T, serve func(*websocket.Conn, *http.Request)) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer ws.Close()
		serve(ws, r)
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v3/tts/unidirectional/stream"
}

func responseFrame(kind byte, event uint32, session string, payload []byte) []byte {
	n := 4 + 4 + 4 + len(session) + 4 + len(payload)
	b := make([]byte, n)
	b[0], b[1], b[2] = 0x11, kind<<4|4, 0x10
	binary.BigEndian.PutUint32(b[4:8], event)
	binary.BigEndian.PutUint32(b[8:12], uint32(len(session)))
	copy(b[12:], session)
	offset := 12 + len(session)
	binary.BigEndian.PutUint32(b[offset:offset+4], uint32(len(payload)))
	copy(b[offset+4:], payload)
	return b
}

func errorFrame(code uint32, payload []byte) []byte {
	b := make([]byte, 12+len(payload))
	b[0], b[1], b[2] = 0x11, 0xf0, 0x10
	binary.BigEndian.PutUint32(b[4:8], code)
	binary.BigEndian.PutUint32(b[8:12], uint32(len(payload)))
	copy(b[12:], payload)
	return b
}

func testRequest() tts.Request {
	return tts.Request{
		Text: "你好，欢迎使用语音合成。",
		Voice: "zh_female_vv_uranus_bigtts",
		Format: audio.Format{
			Encoding: audio.EncodingMP3, SampleRate: 24000, Channels: 1,
		},
	}
}

func TestV3StreamingAndBatch(t *testing.T) {
	results := make(chan error, 2)
	endpoint := mockTTS(t, func(ws *websocket.Conn, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "test-key" ||
			r.Header.Get("X-Api-Resource-Id") != ResourceSeedTTS2 ||
			r.Header.Get("X-Api-Request-Id") == "" {
			results <- errors.New("missing new-console authentication")
			return
		}
		typ, data, err := ws.ReadMessage()
		if err != nil || typ != websocket.BinaryMessage {
			results <- errors.New("missing V3 binary text request")
			return
		}
		if len(data) < 8 || data[0] != 0x11 || data[1] != 0x10 ||
			data[2] != 0x10 || int(binary.BigEndian.Uint32(data[4:8])) != len(data)-8 {
			results <- errors.New("invalid V3 text frame header")
			return
		}
		var body struct {
			Req struct {
				Text string `json:"text"`
				Speaker string `json:"speaker"`
				Audio struct {
					Format string `json:"format"`
					Rate int `json:"sample_rate"`
				} `json:"audio_params"`
			} `json:"req_params"`
		}
		if err := json.Unmarshal(data[8:], &body); err != nil {
			results <- err
			return
		}
		if body.Req.Text != testRequest().Text ||
			body.Req.Speaker != testRequest().Voice ||
			body.Req.Audio.Format != "mp3" || body.Req.Audio.Rate != 24000 {
			results <- errors.New("incorrect TTS request parameters")
			return
		}
		for _, f := range [][]byte{
			responseFrame(kindFullResponse, 350, "session", []byte(`{"text":"你好"}`)),
			responseFrame(kindAudioResponse, eventTTSAudio, "session", []byte{1, 2, 3}),
			responseFrame(kindAudioResponse, eventTTSAudio, "session", []byte{4, 5}),
			responseFrame(kindFullResponse, eventSessionFinished, "session",
				[]byte(`{"status_code":20000000,"message":"ok"}`)),
		} {
			if err := ws.WriteMessage(websocket.BinaryMessage, f); err != nil {
				results <- err
				return
			}
		}
		results <- nil
	})
	client, err := New(Config{APIKey: "test-key", Endpoint: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.SynthesizeStream(ctx, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	for i, want := range [][]byte{{1, 2, 3}, {4, 5}} {
		chunk, err := stream.Recv(ctx)
		if err != nil || string(chunk.Data) != string(want) || chunk.Format != testRequest().Format {
			t.Fatalf("audio %d: %+v, %v", i, chunk, err)
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := stream.Recv(context.Background()); !errors.Is(err, io.EOF) {
			t.Fatalf("stable EOF %d: %v", i, err)
		}
	}
	buffer, err := client.Synthesize(ctx, testRequest())
	if err != nil || string(buffer.Data) != string([]byte{1, 2, 3, 4, 5}) ||
		buffer.Format != testRequest().Format {
		t.Fatalf("batch: %+v / %v", buffer, err)
	}
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
}

func TestV3FrameValidationAndErrors(t *testing.T) {
	for _, raw := range [][]byte{
		nil,
		{0x21, 0xb4, 0x10, 0},
		{0x11, 0xb4, 0x10, 0, 0, 0, 1, 96},
		{0x11, 0xb4, 0x10, 0, 0, 0, 1, 96, 255, 255, 255, 255},
	} {
		if _, err := parseResponse(raw); !speecherr.IsCode(err, speecherr.CodeProtocol) {
			t.Fatalf("malformed frame %v: %v", raw, err)
		}
	}
	for _, tc := range []struct {
		code uint32
		want speecherr.Code
		retry bool
	}{
		{45000000, speecherr.CodeUnauthorized, false},
		{45000151, speecherr.CodeInvalidArgument, false},
		{55000000, speecherr.CodeUnavailable, true},
	} {
		err := remoteError(tc.code, "provider error")
		var detail *speecherr.Error
		if !errors.As(err, &detail) || detail.Code != tc.want || detail.Retryable != tc.retry {
			t.Fatalf("remote %d: %v", tc.code, err)
		}
	}
	frame, err := parseResponse(errorFrame(45000000, []byte(`{"message":"permission denied"}`)))
	if err != nil || !speecherr.IsCode(frame.failure(), speecherr.CodeUnauthorized) {
		t.Fatalf("remote error frame: %v %v", frame, err)
	}
	for _, payload := range []string{
		`{}`,
		`{"status_code":null}`,
		`{"status_code":45000000,"message":"denied"}`,
	} {
		frame := wireFrame{payload: []byte(payload)}
		if err := frame.sessionStatus(); err == nil {
			t.Fatalf("invalid SessionFinished accepted: %s", payload)
		}
	}
}

func TestV3ValidationAndCanceledStream(t *testing.T) {
	for _, cfg := range []Config{
		{},
		{APIKey: "test", Endpoint: "http://example.com/api/v3/tts/unidirectional/stream"},
		{APIKey: "test", Endpoint: "ws://example.com/api/v3/tts/unidirectional/stream"},
		{APIKey: "test", Endpoint: "wss://example.com/not-tts"},
	} {
		if _, err := New(cfg); !speecherr.IsCode(err, speecherr.CodeInvalidArgument) {
			t.Fatalf("invalid config accepted: %+v (%v)", cfg, err)
		}
	}
	for _, req := range []tts.Request{
		{Voice: "v"},
		{Text: "hello"},
		{Text: "hello", Voice: "v", Format: audio.Format{
			Encoding: audio.EncodingWAV, SampleRate: 24000, Channels: 1,
		}},
	} {
		if _, _, err := validateRequest(req); err == nil {
			t.Fatalf("invalid request accepted: %+v", req)
		}
	}
	ready := make(chan struct{}, 1)
	endpoint := mockTTS(t, func(ws *websocket.Conn, _ *http.Request) {
		if _, _, err := ws.ReadMessage(); err != nil {
			return
		}
		ready <- struct{}{}
		_, _, _ = ws.ReadMessage()
	})
	client, err := New(Config{APIKey: "test", Endpoint: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := client.SynthesizeStream(ctx, testRequest())
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	<-ready
	cancel()
	if _, err := stream.Recv(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("parent cancellation = %v", err)
	}
	if _, err := stream.Recv(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("terminal cancellation lost: %v", err)
	}
}
