package tencent

import (
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

func TestTencentWAVNativeWire(t *testing.T) {
	f := audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1}
	wav, err := audio.EncodeWAVPCM16(audio.Buffer{Data: []byte{1, 2, 3, 4}, Format: f})
	if err != nil {
		t.Fatal(err)
	}
	client := testClient(t, func(ws *websocket.Conn, req *http.Request) {
		id := req.URL.Query().Get("voice_id")
		if req.URL.Query().Get("voice_format") != "12" || !verifySignature(req, "secret-key") {
			t.Error("invalid signed WAV request")
			return
		}
		if err := sendResponse(ws, id, 0, 0, nil); err != nil {
			t.Error(err)
			return
		}
		var received []byte
		for {
			typ, data, err := ws.ReadMessage()
			if err != nil {
				t.Error(err)
				return
			}
			if typ == websocket.TextMessage {
				break
			}
			received = append(received, data...)
		}
		if string(received) != string(wav.Data) {
			t.Error("native WAV bytes changed")
		}
		_ = sendResponse(ws, id, 0, 1, nil)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s, err := client.OpenStream(ctx, asr.Config{Format: wav.Format})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Container fragments need not be aligned to PCM sample boundaries.
	if err := s.Send(ctx, wav.Data[:5]); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(ctx, wav.Data[5:]); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseSend(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Recv(ctx); err != io.EOF {
		t.Fatal("expected terminal EOF")
	}
}

func TestTencentWAVModelRestrictions(t *testing.T) {
	f := audio.Format{Encoding: audio.EncodingWAV, SampleRate: 16000, Channels: 1}
	for _, model := range []string{"16k_zh", "16k_zh_en_2.0", "16k_zh_en_speaker_2.0"} {
		if !SupportedAudio(model).Supports(f) {
			t.Fatalf("WAV missing for %s", model)
		}
	}
	if SupportedAudio("Hy-ASR-3.0-preview").Supports(f) {
		t.Fatal("PCM-only preview model advertises WAV")
	}
	c, err := New(Config{AppID: "1", SecretID: "id", SecretKey: "key", InputSampleRate: 8000})
	if err != nil {
		t.Fatal(err)
	}
	f.SampleRate = 8000
	if c.Capabilities().Audio.Supports(f) || !speecherr.IsCode(c.ValidateConfig(asr.Config{Format: f}), speecherr.CodeUnsupported) {
		t.Fatal("PCM-only upsampling accepts WAV")
	}
}
