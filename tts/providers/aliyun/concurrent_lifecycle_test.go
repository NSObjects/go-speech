package aliyun

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/tts"
	"github.com/gorilla/websocket"
)

func TestCosyVoiceCloseUnblocksConcurrentRecv(t *testing.T) {
	peerClosed := make(chan bool, 1)
	ready := make(chan struct{}, 1)
	endpoint := mockTTS(t, func(ws *websocket.Conn, _ *http.Request) {
		if _, err := mockReady(ws); err != nil {
			peerClosed <- false
			return
		}
		ready <- struct{}{}
		_, _, err := ws.ReadMessage()
		peerClosed <- err != nil
	})
	client, err := New(Config{APIKey: "test", Endpoint: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.SynthesizeStream(ctx, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	<-ready
	started := make(chan struct{})
	received := make(chan error, 1)
	go func() {
		close(started)
		_, err := stream.Recv(context.Background())
		received <- err
	}()
	<-started
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-received:
		if !errors.Is(err, tts.ErrStreamClosed) {
			t.Fatalf("concurrent Close did not terminate Recv: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Recv remained blocked after Close")
	}
	for i := 0; i < 2; i++ {
		if _, err := stream.Recv(context.Background()); !errors.Is(err, tts.ErrStreamClosed) {
			t.Fatalf("Recv %d lost terminal close error: %v", i, err)
		}
	}
	select {
	case ok := <-peerClosed:
		if !ok {
			t.Fatal("peer was not disconnected")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("peer remained open after Close")
	}
}

func TestCosyVoiceRecvCancellationUnblocksConnection(t *testing.T) {
	peerClosed := make(chan bool, 1)
	ready := make(chan struct{}, 1)
	endpoint := mockTTS(t, func(ws *websocket.Conn, _ *http.Request) {
		if _, err := mockReady(ws); err != nil {
			peerClosed <- false
			return
		}
		ready <- struct{}{}
		_, _, err := ws.ReadMessage()
		peerClosed <- err != nil
	})
	client, err := New(Config{APIKey: "test", Endpoint: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	parent, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	stream, err := client.SynthesizeStream(parent, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	<-ready
	recvCtx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	received := make(chan error, 1)
	go func() {
		close(started)
		_, err := stream.Recv(recvCtx)
		received <- err
	}()
	<-started
	cancel()
	select {
	case err := <-received:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation cause lost: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Recv remained blocked after cancellation")
	}
	if _, err := stream.Recv(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Recv result was not stable: %v", err)
	}
	select {
	case ok := <-peerClosed:
		if !ok {
			t.Fatal("peer was not disconnected")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("peer remained open after cancellation")
	}
}
