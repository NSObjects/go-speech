package tencent

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/speecherr"
	"github.com/NSObjects/go-speech/tts"
	"github.com/gorilla/websocket"
)

func TestFinalWithoutAudioFailsBothReceiveModes(t *testing.T) {
	for _, events := range []bool{false, true} {
		endpoint := mockServer(t, func(ws *websocket.Conn, req *http.Request) {
			id := req.URL.Query().Get("SessionId")
			if err := sendControl(ws, id, 0, 1, 0); err != nil {
				return
			}
			if err := readCommand(ws, "ACTION_SYNTHESIS", id, sampleRequest().Text); err != nil {
				return
			}
			if err := readCommand(ws, "ACTION_COMPLETE", id, ""); err != nil {
				return
			}
			_ = sendControl(ws, id, 0, 0, 1)
		})
		c, err := New(Config{AppID: "1", SecretID: "id", SecretKey: "key", Endpoint: endpoint})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		s, err := c.SynthesizeStream(ctx, sampleRequest())
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		receive := func() error {
			if events {
				_, err := s.(tts.EventReceiver).RecvEvent(ctx)
				return err
			}
			_, err := s.Recv(ctx)
			return err
		}
		first := receive()
		second := receive()
		_ = s.Close()
		cancel()
		if !speecherr.IsCode(first, speecherr.CodeProtocol) || second != first {
			t.Fatalf("empty stream: first=%v second=%v", first, second)
		}
	}
}
