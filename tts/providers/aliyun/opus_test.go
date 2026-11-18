package aliyun

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/NSObjects/go-speech/tts"
	"github.com/gorilla/websocket"
)

func TestCosyVoiceOggOpusCapabilityAndFormat(t *testing.T) {
	want := audio.Format{Encoding: audio.EncodingOpus, SampleRate: 24000, Channels: 1}
	if err := SupportedAudio().Validate(want); err != nil {
		t.Fatalf("Ogg Opus must be advertised: %v", err)
	}
	request, providerFormat, err := validateRequest(tts.Request{
		Text: "你好", Voice: "longanyang", Format: want,
	})
	if err != nil || request.Format != want || providerFormat != "opus" {
		t.Fatalf("Ogg Opus mapping wrong: %+v / %q / %v", request, providerFormat, err)
	}
	if err := SupportedAudio().Validate(audio.Format{
		Encoding: audio.EncodingOpus, SampleRate: 24000, Channels: 2,
	}); !errors.Is(err, audio.ErrUnsupportedFormat) {
		t.Fatalf("stereo Opus must be rejected: %v", err)
	}
}

func TestCosyVoiceOggOpusPreservesBinaryFragments(t *testing.T) {
	seen := make(chan error, 1)
	endpoint := mockTTS(t, func(ws *websocket.Conn, _ *http.Request) {
		run, err := readTTSCommand(ws, "run-task", "")
		if err != nil {
			seen <- err
			return
		}
		params, ok := run["parameters"].(map[string]any)
		if !ok || params["format"] != "opus" || params["sample_rate"] != float64(24000) {
			seen <- errors.New("wrong Opus task parameters")
			return
		}
		taskID := run["__task_id"].(string)
		if err := sendTTSControl(ws, "task-started", taskID, ""); err != nil {
			seen <- err
			return
		}
		if _, err := readTTSCommand(ws, "continue-task", taskID); err != nil {
			seen <- err
			return
		}
		if _, err := readTTSCommand(ws, "finish-task", taskID); err != nil {
			seen <- err
			return
		}
		for _, raw := range [][]byte{[]byte("Og"), []byte("gS"), []byte{0, 1, 2, 3}} {
			if err := ws.WriteMessage(websocket.BinaryMessage, raw); err != nil {
				seen <- err
				return
			}
		}
		if err := sendTTSControl(ws, "task-finished", taskID, ""); err != nil {
			seen <- err
			return
		}
		seen <- nil
	})
	client, err := New(Config{APIKey: "test", Endpoint: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req := tts.Request{
		Text: "你好", Voice: "longanyang",
		Format: audio.Format{Encoding: audio.EncodingOpus, SampleRate: 24000, Channels: 1},
	}
	s, err := client.SynthesizeStream(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var output []byte
	for i, expected := range [][]byte{[]byte("Og"), []byte("gS"), []byte{0, 1, 2, 3}} {
		chunk, err := s.Recv(ctx)
		if err != nil || !bytes.Equal(chunk.Data, expected) || chunk.Format != req.Format {
			t.Fatalf("binary fragment %d corrupted: %+v, %v", i, chunk, err)
		}
		output = append(output, chunk.Data...)
	}
	if !bytes.Equal(output, []byte{'O', 'g', 'g', 'S', 0, 1, 2, 3}) {
		t.Fatalf("unexpected concatenated bitstream: %x", output)
	}
	if _, err := s.Recv(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("missing terminal EOF: %v", err)
	}
	if err := <-seen; err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Recv(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("unstable terminal EOF: %v", err)
	}
	if _, err := client.SynthesizeStream(context.Background(), tts.Request{
		Text: "你好", Voice: "longanyang", Format: audio.Format{
			Encoding: audio.EncodingAAC, SampleRate: 24000, Channels: 1,
		},
	}); !speecherr.IsCode(err, speecherr.CodeUnsupported) {
		t.Fatalf("unsupported encoding not rejected: %v", err)
	}
}
