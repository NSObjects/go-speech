package volcengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/NSObjects/go-speech/tts"
	"github.com/gorilla/websocket"
)

const (
	fixtureVoice = "PersonaOS_fixture"
	fixtureKey   = "fixture-key"
	readyJSON    = `{"event":"ready","session_id":"fixture"}`
	audioJSON    = `{"event":"audio","audio":"AQID"}`
	finalJSON    = `{"event":"final","is_final":true,"reason":"TTS_STREAM_FINAL_REASON_CLOSED"}`
)

func fixtureClient(t *testing.T, h http.Handler) *Client {
	t.Helper()
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	client, err := New(Config{APIKey: fixtureKey, Endpoint: server.URL, StartTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestHTTPStreamingAndBatch(t *testing.T) {
	client := fixtureClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v3/text-to-speech/"+fixtureVoice+"/streaming" ||
			r.Header.Get("X-Api-Key") != fixtureKey {
			t.Error("wrong HTTP synthesis request")
		}
		var req synthesisRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ModelID != defaultModel ||
			req.Text != "你好" || req.OutputFormat.Format != "ogg_opus" || req.OutputFormat.SampleRate != 48000 {
			t.Errorf("unexpected request %+v, %v", req, err)
		}
		_, _ = fmt.Fprintln(w, readyJSON)
		_, _ = fmt.Fprint(w, `{"event":"audio","au`)
		w.(http.Flusher).Flush()
		_, _ = fmt.Fprintln(w, `dio":"AQID"}`)
		_, _ = fmt.Fprintln(w, audioJSON)
		_, _ = fmt.Fprintln(w, finalJSON)
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	format := audio.Format{Encoding: audio.EncodingOpus, SampleRate: 48000, Channels: 1}
	req := tts.Request{Text: "你好", Voice: fixtureVoice, Format: format}
	out, err := client.Synthesize(ctx, req)
	if err != nil || out.Format != format || string(out.Data) != "\x01\x02\x03\x01\x02\x03" {
		t.Fatalf("batch=%+v err=%v", out, err)
	}
	s, err := client.SynthesizeStream(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for range 2 {
		chunk, err := s.Recv(ctx)
		if err != nil || len(chunk.Data) != 3 || chunk.Format != format {
			t.Fatalf("chunk=%+v err=%v", chunk, err)
		}
	}
	for range 2 {
		if _, err := s.Recv(ctx); !errors.Is(err, io.EOF) {
			t.Fatalf("terminal=%v", err)
		}
	}
}

func TestHTTPRejectsPrematureEOFAndEmptyAudio(t *testing.T) {
	for _, body := range []string{readyJSON + "\n" + audioJSON, readyJSON + "\n" + finalJSON, readyJSON + "\n" + `{"event":"audio","audio":"invalid!"}`} {
		client := fixtureClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprintln(w, body)
		}))
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		s, err := client.SynthesizeStream(ctx, tts.Request{Text: "你好", Voice: fixtureVoice})
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		for err == nil {
			_, err = s.Recv(ctx)
		}
		if !speecherr.IsCode(err, speecherr.CodeProtocol) {
			t.Errorf("premature or invalid stream: %v", err)
		}
		_, retained := s.Recv(ctx)
		if retained != err {
			t.Errorf("terminal error not retained: %v", retained)
		}
		_ = s.Close()
		cancel()
	}
}

func TestDuplexFlushAndClose(t *testing.T) {
	upgrader := websocket.Upgrader{}
	client := fixtureClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/text-to-speech/"+fixtureVoice+"/streaming-input" || r.Header.Get("X-Api-Key") != fixtureKey {
			t.Error("wrong websocket endpoint or auth")
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(4 * time.Second))
		var init clientEvent
		if err := conn.ReadJSON(&init); err != nil || init.Event != "init" || init.OutputFormat == nil || init.OutputFormat.Format != "mp3" {
			t.Errorf("init=%+v err=%v", init, err)
			return
		}
		if err := conn.WriteJSON(event{Event: "ready", SessionID: init.SessionID}); err != nil {
			t.Error(err)
			return
		}
		for _, expected := range []string{"text", "flush", "text", "close"} {
			var in clientEvent
			if err := conn.ReadJSON(&in); err != nil || in.Event != expected {
				t.Errorf("want %s, got %+v, %v", expected, in, err)
				return
			}
			if expected == "flush" || expected == "close" {
				reason := flushedReason
				if expected == "close" {
					reason = closedReason
				}
				if err := conn.WriteJSON(event{Event: "audio", Audio: []byte{1, 2, 3}}); err != nil {
					t.Error(err)
					return
				}
				if err := conn.WriteJSON(event{Event: "final", IsFinal: true, Reason: reason}); err != nil {
					t.Error(err)
					return
				}
			}
		}
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := client.OpenDuplex(ctx, tts.DuplexRequest{Voice: fixtureVoice})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SendText(ctx, "第一段"); err != nil {
		t.Fatal(err)
	}
	if err := s.(*duplex).Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if chunk, err := s.Recv(ctx); err != nil || len(chunk.Data) != 3 {
		t.Fatalf("audio before closing input: %+v %v", chunk, err)
	}
	if err := s.SendText(ctx, "第二段"); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseSend(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.SendText(ctx, "late"); !errors.Is(err, tts.ErrInputClosed) {
		t.Fatalf("send after close=%v", err)
	}
	if chunk, err := s.Recv(ctx); err != nil || len(chunk.Data) != 3 {
		t.Fatalf("second audio=%+v err=%v", chunk, err)
	}
	for range 2 {
		if _, err := s.Recv(ctx); !errors.Is(err, io.EOF) {
			t.Fatalf("terminal=%v", err)
		}
	}
}

func TestHTTPReadCancellation(t *testing.T) {
	client := fixtureClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintln(w, readyJSON)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s, err := client.SynthesizeStream(ctx, tts.Request{Text: "你好", Voice: fixtureVoice})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	readCtx, stop := context.WithTimeout(ctx, 30*time.Millisecond)
	defer stop()
	if _, err := s.Recv(readCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("read cancellation=%v", err)
	}
	if _, err := s.Recv(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("retained cancellation=%v", err)
	}
}

func TestOpusFormatAndEndpointValidation(t *testing.T) {
	if _, _, err := validateOutput(fixtureVoice, audio.Format{Encoding: audio.EncodingOpus, SampleRate: 24000, Channels: 1}); err == nil {
		t.Fatal("invalid Opus sample rate accepted")
	}
	for _, endpoint := range []string{"http://example.com", "https://example.com/legacy", "https://user@example.com"} {
		if _, err := New(Config{APIKey: fixtureKey, Endpoint: endpoint}); err == nil {
			t.Errorf("unsafe endpoint accepted: %s", endpoint)
		}
	}
}
