package aliyun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("WebSocket upgrade: %v", err)
			return
		}
		defer conn.Close()
		serve(conn, r)
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http") + "/api-ws/v1/inference"
}
func sendTTSControl(ws *websocket.Conn, event, id, code string) error {
	body, err := json.Marshal(map[string]any{
		"header": map[string]any{
			"event": event, "task_id": id, "error_code": code,
		},
		"payload": map[string]any{},
	})
	if err != nil {
		return err
	}
	return ws.WriteMessage(websocket.TextMessage, body)
}
func readTTSCommand(ws *websocket.Conn, name, id string) (map[string]any, error) {
	typ, data, err := ws.ReadMessage()
	if err != nil {
		return nil, err
	}
	if typ != websocket.TextMessage {
		return nil, fmt.Errorf("wanted text command %s, got type %d", name, typ)
	}
	var event struct {
		Header struct {
			Action    string `json:"action"`
			TaskID    string `json:"task_id"`
			Streaming string `json:"streaming"`
		} `json:"header"`
		Payload map[string]any `json:"payload"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, err
	}
	if event.Header.Action != name || event.Header.Streaming != "duplex" ||
		(id != "" && event.Header.TaskID != id) || event.Header.TaskID == "" {
		return nil, fmt.Errorf("invalid %s command: %+v", name, event.Header)
	}
	event.Payload["__task_id"] = event.Header.TaskID
	return event.Payload, nil
}
func testRequest() tts.Request {
	return tts.Request{
		Text:   "你好，欢迎使用语音合成。",
		Voice:  "longanyang",
		Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 22050, Channels: 1},
	}
}

func TestCosyVoiceStreamAndBatchRoundTrip(t *testing.T) {
	observed := make(chan error, 2)
	endpoint := mockTTS(t, func(ws *websocket.Conn, request *http.Request) {
		report := func(err error) { observed <- err }
		if request.Header.Get("Authorization") != "Bearer test-key" ||
			request.Header.Get("X-DashScope-WorkSpace") != "test-workspace" {
			report(errors.New("incorrect authorization or workspace"))
			return
		}
		run, err := readTTSCommand(ws, "run-task", "")
		if err != nil {
			report(err)
			return
		}
		task := run["__task_id"].(string)
		params, ok := run["parameters"].(map[string]any)
		if !ok || params["format"] != "pcm" || params["sample_rate"] != float64(22050) ||
			params["voice"] != "longanyang" || params["text_type"] != "PlainText" ||
			run["model"] != ModelCosyVoiceV3Flash || run["function"] != "SpeechSynthesizer" {
			report(fmt.Errorf("invalid run-task payload: %#v", run))
			return
		}
		if err := sendTTSControl(ws, "task-started", task, ""); err != nil {
			report(err)
			return
		}
		cont, err := readTTSCommand(ws, "continue-task", task)
		if err != nil {
			report(err)
			return
		}
		in, ok := cont["input"].(map[string]any)
		if !ok || in["text"] != testRequest().Text {
			report(fmt.Errorf("invalid text payload: %#v", cont))
			return
		}
		end, err := readTTSCommand(ws, "finish-task", task)
		if err != nil {
			report(err)
			return
		}
		if _, ok := end["input"].(map[string]any); !ok {
			report(fmt.Errorf("finish-task missing input: %#v", end))
			return
		}
		if err := sendTTSControl(ws, "result-generated", task, ""); err != nil {
			report(err)
			return
		}
		if err := ws.WriteMessage(websocket.BinaryMessage, []byte{1, 2, 3, 4}); err != nil {
			report(err)
			return
		}
		if err := sendTTSControl(ws, "result-generated", task, ""); err != nil {
			report(err)
			return
		}
		if err := ws.WriteMessage(websocket.BinaryMessage, []byte{5, 6}); err != nil {
			report(err)
			return
		}
		if err := sendTTSControl(ws, "task-finished", task, ""); err != nil {
			report(err)
			return
		}
		report(nil)
	})
	client, err := New(Config{
		APIKey: "test-key", Endpoint: endpoint, WorkspaceID: "test-workspace",
	})
	if err != nil {
		t.Fatal(err)
	}
	registry := tts.NewRegistry()
	if err := registry.Register("cosyvoice", tts.Provider{Batch: client, Stream: client}); err != nil {
		t.Fatal(err)
	}
	provider, ok := registry.Lookup("cosyvoice")
	if !ok || provider.Batch == nil || provider.Stream == nil {
		t.Fatal("missing TTS capabilities")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := provider.Stream.SynthesizeStream(ctx, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range [][]byte{{1, 2, 3, 4}, {5, 6}} {
		got, err := stream.Recv(ctx)
		if err != nil {
			t.Fatalf("chunk %d: %v", i, err)
		}
		if string(got.Data) != string(want) || got.Format != testRequest().Format {
			t.Fatalf("chunk %d: %#v; expected %v", i, got, want)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := stream.Recv(context.Background()); !errors.Is(err, io.EOF) {
			t.Fatalf("Recv after completion %d: %v", i, err)
		}
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	buffer, err := provider.Batch.Synthesize(ctx, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if string(buffer.Data) != string([]byte{1, 2, 3, 4, 5, 6}) || buffer.Format != testRequest().Format {
		t.Fatalf("batch result mismatch: %#v", buffer)
	}
	for i := 0; i < 2; i++ {
		if err := <-observed; err != nil {
			t.Fatal(err)
		}
	}
}

func TestCosyVoiceRequestValidation(t *testing.T) {
	endpoint := "ws://localhost/api-ws/v1/inference"
	client, err := New(Config{APIKey: "k", Endpoint: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		request  tts.Request
		code     speecherr.Code
		sentinel error
	}{
		{"empty text", tts.Request{Voice: "longanyang"}, speecherr.CodeInvalidArgument, nil},
		{"empty voice", tts.Request{Text: "你好"}, speecherr.CodeInvalidArgument, nil},
		{"invalid sample rate", tts.Request{Text: "你好", Voice: "longanyang", Format: audio.Format{
			Encoding: audio.EncodingPCM16LE, SampleRate: 0, Channels: 1,
		}}, speecherr.CodeInvalidArgument, audio.ErrInvalidFormat},
		{"unsupported format", tts.Request{Text: "你好", Voice: "longanyang", Format: audio.Format{
			Encoding: audio.EncodingAMR, SampleRate: 22050, Channels: 1,
		}}, speecherr.CodeUnsupported, audio.ErrUnsupportedFormat},
		{"stereo", tts.Request{Text: "你好", Voice: "longanyang", Format: audio.Format{
			Encoding: audio.EncodingMP3, SampleRate: 22050, Channels: 2,
		}}, speecherr.CodeUnsupported, audio.ErrUnsupportedFormat},
		{"too long", tts.Request{Text: strings.Repeat("中", 20001), Voice: "longanyang"}, speecherr.CodeInvalidArgument, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.SynthesizeStream(context.Background(), tc.request)
			if !speecherr.IsCode(err, tc.code) {
				t.Fatalf("expected error %s, got %v", tc.code, err)
			}
			if tc.sentinel != nil && !errors.Is(err, tc.sentinel) {
				t.Fatalf("expected errors.Is(%v), got %v", tc.sentinel, err)
			}
		})
	}
	def, format, err := validateRequest(tts.Request{Text: "hi", Voice: "longanyang"})
	if err != nil || format != "mp3" || def.Format != (audio.Format{
		Encoding: audio.EncodingMP3, SampleRate: 22050, Channels: 1,
	}) {
		t.Fatalf("unexpected default format: %#v %s %v", def.Format, format, err)
	}
	for _, cfg := range []Config{
		{}, {APIKey: "k", Endpoint: "ws://not-loopback.example/api-ws/v1/inference"},
		{APIKey: "k", Endpoint: "https://server.example/api-ws/v1/inference"},
		{APIKey: "k", Endpoint: "wss://server.example/not-the-endpoint"},
		{APIKey: "k", Endpoint: endpoint, StartTimeout: -1},
	} {
		if _, err := New(cfg); !speecherr.IsCode(err, speecherr.CodeInvalidArgument) {
			t.Fatalf("invalid config %+v: %v", cfg, err)
		}
	}
}

func TestCosyVoiceTaskFailedRetainsCode(t *testing.T) {
	endpoint := mockTTS(t, func(ws *websocket.Conn, _ *http.Request) {
		run, err := readTTSCommand(ws, "run-task", "")
		if err != nil {
			return
		}
		id := run["__task_id"].(string)
		if err := sendTTSControl(ws, "task-started", id, ""); err != nil {
			return
		}
		if _, err := readTTSCommand(ws, "continue-task", id); err != nil {
			return
		}
		if _, err := readTTSCommand(ws, "finish-task", id); err != nil {
			return
		}
		_ = sendTTSControl(ws, "task-failed", id, "Throttling")
	})
	client, err := New(Config{APIKey: "k", Endpoint: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := client.SynthesizeStream(ctx, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, first := s.Recv(ctx)
	if !speecherr.IsCode(first, speecherr.CodeRateLimited) {
		t.Fatalf("expected rate limited remote error: %v", first)
	}
	var e *speecherr.Error
	if !errors.As(first, &e) || e.RemoteCode != "Throttling" || !e.Retryable {
		t.Fatalf("remote error detail lost: %v", first)
	}
	_, second := s.Recv(context.Background())
	if second != first {
		t.Fatalf("original remote failure changed: %v and %v", first, second)
	}
}

func TestCosyVoiceCancellationReleasesStream(t *testing.T) {
	freed := make(chan bool, 1)
	endpoint := mockTTS(t, func(ws *websocket.Conn, _ *http.Request) {
		run, err := readTTSCommand(ws, "run-task", "")
		if err != nil {
			freed <- false
			return
		}
		id := run["__task_id"].(string)
		if err := sendTTSControl(ws, "task-started", id, ""); err != nil {
			freed <- false
			return
		}
		for i := 0; i < 2; i++ {
			if _, _, err := ws.ReadMessage(); err != nil {
				freed <- false
				return
			}
		}
		_, _, err = ws.ReadMessage()
		freed <- err != nil
	})
	client, err := New(Config{APIKey: "k", Endpoint: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	parent, cancel := context.WithCancel(context.Background())
	s, err := client.SynthesizeStream(parent, testRequest())
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.Recv(context.Background()); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation lost: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Recv not released after parent cancellation")
	}
	if _, err := s.Recv(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("unstable canceled terminal state: %v", err)
	}
	select {
	case closed := <-freed:
		if !closed {
			t.Fatal("server socket was not released")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server was not released after cancellation")
	}
}
