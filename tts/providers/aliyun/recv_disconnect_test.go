package aliyun

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/speecherr"
	"github.com/gorilla/websocket"
)

func TestCosyVoiceAbruptDisconnectReturnsStableUnavailable(t *testing.T) {
	endpoint := mockTTS(t, func(ws *websocket.Conn, _ *http.Request) {
		_, _ = mockReady(ws)
		// No task-finished: closing the socket must not report io.EOF.
	})
	client, err := New(Config{APIKey: "test", Endpoint: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := client.SynthesizeStream(ctx, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	_, first := stream.Recv(ctx)
	if !speecherr.IsCode(first, speecherr.CodeUnavailable) {
		t.Fatalf("disconnect unexpectedly treated as success: %v", first)
	}
	_, again := stream.Recv(context.Background())
	if first != again {
		t.Fatalf("disconnect cause changed: first=%v again=%v", first, again)
	}
}
