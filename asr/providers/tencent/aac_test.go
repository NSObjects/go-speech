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
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/gorilla/websocket"
)

func testADTSFrame(size int) []byte {
	frame := make([]byte, size)
	copy(frame, []byte{0xff, 0xf1, 0x60, 0x40, 0, 0x1f, 0xfc})
	frame[3] |= byte(size >> 11)
	frame[4] = byte(size >> 3)
	frame[5] |= byte(size & 7) << 5
	return frame
}

func TestAACFrames(t *testing.T) {
	a, b := testADTSFrame(17), testADTSFrame(42)
	raw := append(bytes.Clone(a), b...)
	frames, err := aacFrames(raw)
	if err != nil || len(frames) != 2 || !bytes.Equal(frames[0], a) || !bytes.Equal(frames[1], b) {
		t.Fatalf("AAC frames: %v", err)
	}
	crc := bytes.Clone(a)
	crc[1] &^= 1
	if _, err := aacFrames(crc); err != nil {
		t.Fatalf("CRC frame: %v", err)
	}
	for _, bad := range [][]byte{nil, a[:6], a[:16], testADTSFrame(7), append(bytes.Clone(a), 1), bytes.Repeat(a, 938)} {
		if _, err := aacFrames(bad); err == nil {
			t.Fatal("accepted invalid AAC input")
		}
	}
	for _, index := range []int{0, 1, 2, 3, 6} {
		bad := bytes.Clone(a)
		switch index {
		case 0:
			bad[0] = 0
		case 1:
			bad[1] |= 8
		case 2:
			bad[2] = 0x64
		case 3:
			bad[3] = 0x80
		case 6:
			bad[6] |= 1
		}
		if _, err := aacFrames(bad); err == nil {
			t.Fatalf("accepted bad header at %d", index)
		}
	}
	frames, err = aacFrames(bytes.Repeat(a, 937))
	if err != nil || len(frames) != 937 {
		t.Fatalf("duration limit: %v", err)
	}
}

func TestAACNativeWire(t *testing.T) {
	frame := testADTSFrame(17)
	client := testClient(t, func(ws *websocket.Conn, req *http.Request) {
		id := req.URL.Query().Get("voice_id")
		if req.URL.Query().Get("voice_format") != "16" || !verifySignature(req, "secret-key") {
			t.Error("incorrect AAC signed query")
			return
		}
		if err := sendResponse(ws, id, 0, 0, nil); err != nil {
			t.Error(err)
			return
		}
		for _, want := range [][]byte{frame[:5], frame[5:]} {
			typ, got, err := ws.ReadMessage()
			if err != nil || typ != websocket.BinaryMessage || !bytes.Equal(got, want) {
				t.Errorf("AAC fragment mismatch: %v", err)
				return
			}
		}
		typ, got, err := ws.ReadMessage()
		if err != nil || typ != websocket.TextMessage || string(got) != `{"type":"end"}` {
			t.Errorf("AAC close: %v", err)
			return
		}
		if err := sendResponse(ws, id, 0, 1, nil); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cfg := asr.Config{Format: audio.Format{Encoding: audio.EncodingAAC, SampleRate: 16000, Channels: 1}}
	if !client.Capabilities().Audio.Supports(cfg.Format) {
		t.Fatal("AAC missing from capabilities")
	}
	if _, err := client.Recognize(ctx, audio.Buffer{Data: frame[:6], Format: cfg.Format}, cfg); !speecherr.IsCode(err, speecherr.CodeInvalidArgument) {
		t.Fatalf("invalid AAC: %v", err)
	}
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
