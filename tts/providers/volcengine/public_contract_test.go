package volcengine

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/tts"
	"github.com/gorilla/websocket"
)

func TestVolcenginePublicStreamClosedError(t *testing.T) {
	requestRead := make(chan struct{})
	endpoint := mockTTS(t, func(ws *websocket.Conn, _ *http.Request) {
		if _, _, err := ws.ReadMessage(); err != nil {
			return
		}
		close(requestRead)
		_, _, _ = ws.ReadMessage()
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
	select {
	case <-requestRead:
	case <-ctx.Done():
		t.Fatalf("server did not receive synthesis request: %v", ctx.Err())
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		_, err := stream.Recv(context.Background())
		if !errors.Is(err, tts.ErrStreamClosed) || errors.Is(err, io.EOF) {
			t.Fatalf("Recv after explicit Close (%d): %v", i, err)
		}
	}
}

var _ tts.Client = (*Client)(nil)
