package aliyun

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/speecherr"
	"github.com/gorilla/websocket"
)

func TestCosyVoiceEmptyStreamIsStableProtocolFailure(t *testing.T) {
	endpoint := mockTTS(t, func(ws *websocket.Conn, _ *http.Request) {
		id, err := mockReady(ws)
		if err == nil {
			_ = sendTTSControl(ws, "task-finished", id, "")
		}
	})
	client, err := New(Config{APIKey: "test", Endpoint: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s, err := client.SynthesizeStream(ctx, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, first := s.Recv(ctx)
	if !speecherr.IsCode(first, speecherr.CodeProtocol) {
		t.Fatalf("empty stream accepted as success: %v", first)
	}
	_, again := s.Recv(context.Background())
	if first != again {
		t.Fatalf("terminal protocol error changed: %v, %v", first, again)
	}
}
