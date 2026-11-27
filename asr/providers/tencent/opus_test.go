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

func TestNativeOpusFrame(t *testing.T) {
	packet := []byte{0x10, 1, 2}
	frame, err := FrameOpusPacket(packet)
	if err != nil || !bytes.Equal(frame, []byte{'o', 'p', 'u', 's', 3, 0, 0x10, 1, 2}) {
		t.Fatalf("frame=%x err=%v", frame, err)
	}
	packet[1] = 9
	if frame[7] != 1 {
		t.Fatal("frame retained caller memory")
	}
	for _, p := range [][]byte{nil, {0x14, 1}, {0x08, 1}, {0x11, 1}, make([]byte, 1276)} {
		if _, err := FrameOpusPacket(p); !speecherr.IsCode(err, speecherr.CodeInvalidArgument) {
			t.Fatalf("accepted invalid packet: %v", err)
		}
	}
	for _, raw := range [][]byte{nil, []byte("OggS"), frame[:5], frame[:len(frame)-1], append(bytes.Clone(frame), 0)} {
		if _, err := nativeOpusFrames(raw); !speecherr.IsCode(err, speecherr.CodeInvalidArgument) {
			t.Fatalf("accepted malformed buffer: %v", err)
		}
	}
	if _, err := nativeOpusFrames(bytes.Repeat(frame, 1501)); err == nil {
		t.Fatal("accepted more than 60 seconds")
	}
	frames, err := nativeOpusFrames(bytes.Repeat(frame, 1500))
	if err != nil || len(frames) != 1500 {
		t.Fatalf("60 second boundary: %v", err)
	}
}

func TestNativeOpusWire(t *testing.T) {
	frame, err := FrameOpusPacket([]byte{0x10, 1, 2})
	if err != nil {
		t.Fatal(err)
	}
	c := testClient(t, func(ws *websocket.Conn, req *http.Request) {
		id := req.URL.Query().Get("voice_id")
		if req.URL.Query().Get("voice_format") != "10" || !verifySignature(req, "secret-key") {
			t.Error("wrong native Opus signed request")
			return
		}
		if err := sendResponse(ws, id, 0, 0, nil); err != nil {
			t.Error(err)
			return
		}
		for i := 0; i < 2; i++ {
			typ, data, err := ws.ReadMessage()
			if err != nil || typ != websocket.BinaryMessage || !bytes.Equal(data, frame) {
				t.Errorf("frame %d mismatch: %v", i, err)
				return
			}
		}
		typ, data, err := ws.ReadMessage()
		if err != nil || typ != websocket.TextMessage || string(data) != `{"type":"end"}` {
			t.Errorf("end frame: %v", err)
			return
		}
		if err := sendResponse(ws, id, 0, 1, nil); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	f := audio.Format{Encoding: EncodingOpusFrames, SampleRate: 16000, Channels: 1}
	s, err := c.OpenStream(ctx, asr.Config{Format: f})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Send(ctx, frame[:5]); !speecherr.IsCode(err, speecherr.CodeInvalidArgument) {
		t.Fatalf("invalid partial frame: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := s.Send(ctx, frame); err != nil {
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

func TestNativeOpusCapability(t *testing.T) {
	f := audio.Format{Encoding: EncodingOpusFrames, SampleRate: 16000, Channels: 1}
	if !SupportedAudio("16k_zh").Supports(f) {
		t.Fatal("verified native Opus not advertised")
	}
	for _, model := range []string{"8k_zh", "16k_zh_en_2.0", "Hy-ASR-3.0-preview"} {
		if SupportedAudio(model).Supports(f) {
			t.Fatalf("unverified model advertises native Opus: %s", model)
		}
	}
	f.Encoding = audio.EncodingOpus
	if SupportedAudio("16k_zh").Supports(f) {
		t.Fatal("Ogg Opus advertised as native framing")
	}
}
