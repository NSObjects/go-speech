package tencent

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

// The public TTS contract must not require callers to inspect private
// provider errors to tell explicit Close apart from successful EOF.
func TestTencentPublicStreamClosedError(t *testing.T) {
	commandsRead := make(chan struct{})
	endpoint := mockServer(t, func(ws *websocket.Conn, req *http.Request) {
		session := req.URL.Query().Get("SessionId")
		if err := sendControl(ws, session, 0, 1, 0); err != nil {
			return
		}
		if err := readCommand(ws, "ACTION_SYNTHESIS", session, sampleRequest().Text); err != nil {
			return
		}
		if err := readCommand(ws, "ACTION_COMPLETE", session, ""); err != nil {
			return
		}
		close(commandsRead)
		_, _, _ = ws.ReadMessage()
	})
	client, err := New(Config{AppID: "1", SecretID: "id", SecretKey: "key", Endpoint: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := client.SynthesizeStream(ctx, sampleRequest())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-commandsRead:
	case <-ctx.Done():
		t.Fatalf("server did not receive synthesis commands: %v", ctx.Err())
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
