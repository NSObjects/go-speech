package tencent

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/gorilla/websocket"
)

func TestMP3FramePacingInput(t *testing.T) {
	// MPEG-2 Layer III, 16 kHz mono, 32kb/s => 144-byte frame.
	frame := make([]byte, 144)
	copy(frame, []byte{0xff, 0xf3, 0x48, 0xc0})
	if size, err := mp3FrameSize(frame); err != nil || size != 144 {
		t.Fatalf("frame size=%d err=%v", size, err)
	}
	tag := []byte{'I', 'D', '3', 4, 0, 0, 0, 0, 0, 1, 0}
	trailer := make([]byte, 128)
	copy(trailer, "TAG")
	raw := append(bytes.Clone(tag), frame...)
	raw = append(raw, frame...)
	raw = append(raw, trailer...)
	frames, err := mp3Frames(raw)
	if err != nil || len(frames) != 2 || !bytes.Equal(bytes.Join(frames, nil), raw) {
		t.Fatalf("MP3 framing: %v", err)
	}
	for _, bad := range [][]byte{nil, frame[:3], frame[:143], tag, append(bytes.Clone(frame), 1), bytes.Repeat(frame, 1667)} {
		if _, err := mp3Frames(bad); err == nil {
			t.Fatal("accepted invalid MP3 buffer")
		}
	}
	frames, err = mp3Frames(bytes.Repeat(frame, 1666))
	if err != nil || len(frames) != 1666 {
		t.Fatalf("60s bound: %v", err)
	}
	stereo := bytes.Clone(frame)
	stereo[3] = 0
	if _, err := mp3Frames(stereo); err == nil {
		t.Fatal("accepted stereo MP3")
	}
}

func TestMP3NativeWire(t *testing.T) {
	frame := make([]byte, 144)
	copy(frame, []byte{0xff, 0xf3, 0x48, 0xc0})
	client := testClient(t, func(ws *websocket.Conn, req *http.Request) {
		id := req.URL.Query().Get("voice_id")
		if req.URL.Query().Get("voice_format") != "8" || !verifySignature(req, "secret-key") {
			t.Error("incorrect MP3 signed query")
			return
		}
		if err := sendResponse(ws, id, 0, 0, nil); err != nil {
			t.Error(err)
			return
		}
		for _, want := range [][]byte{frame[:5], frame[5:]} {
			typ, got, err := ws.ReadMessage()
			if err != nil || typ != websocket.BinaryMessage || !bytes.Equal(got, want) {
				t.Errorf("MP3 fragment mismatch: %v", err)
				return
			}
		}
		typ, got, err := ws.ReadMessage()
		if err != nil || typ != websocket.TextMessage || string(got) != `{"type":"end"}` {
			t.Errorf("MP3 close: %v", err)
			return
		}
		if err := sendResponse(ws, id, 0, 1, nil); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cfg := asr.Config{Format: audio.Format{Encoding: audio.EncodingMP3, SampleRate: 16000, Channels: 1}}
	s, err := client.OpenStream(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, fragment := range [][]byte{frame[:5], frame[5:]} {
		if err := s.Send(ctx, fragment); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CloseSend(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := s.Recv(ctx); err != io.EOF {
			t.Fatalf("EOF %d: %v", i, err)
		}
	}
}
