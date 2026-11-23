package volcengine

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/speecherr"
	"github.com/gorilla/websocket"
)

func TestVolcengineRecvDeadlineReleasesPeerAndRetainsCause(t *testing.T) {
	released := make(chan bool, 1)
	endpoint := mockTTS(t, func(ws *websocket.Conn, _ *http.Request) {
		if _, _, err := ws.ReadMessage(); err != nil {
			released <- false
			return
		}
		_, _, err := ws.ReadMessage()
		released <- err != nil
	})
	client, err := New(Config{APIKey: "test", Endpoint: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	parent, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := client.SynthesizeStream(parent, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	recvCtx, stop := context.WithTimeout(context.Background(), 70*time.Millisecond)
	_, first := stream.Recv(recvCtx)
	stop()
	if !errors.Is(first, context.DeadlineExceeded) {
		t.Fatalf("Recv lost deadline cause: %v", first)
	}
	for i := 0; i < 2; i++ {
		_, again := stream.Recv(context.Background())
		if !errors.Is(again, context.DeadlineExceeded) {
			t.Fatalf("Recv %d lost terminal deadline: %v", i, again)
		}
	}
	select {
	case ok := <-released:
		if !ok {
			t.Fatal("peer was not released")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("peer remained blocked after Recv deadline")
	}
}

func TestVolcengineAbruptDisconnectReturnsStableUnavailable(t *testing.T) {
	endpoint := mockTTS(t, func(ws *websocket.Conn, _ *http.Request) {
		_, _, _ = ws.ReadMessage()
		// Return without SessionFinished: an abrupt WebSocket disconnect.
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
