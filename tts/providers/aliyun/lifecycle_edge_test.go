package aliyun

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/speecherr"
	"github.com/gorilla/websocket"
)

// mockReady completes the real protocol handshake but does not synthesize.
func mockReady(ws *websocket.Conn) (string, error) {
	command, err := readTTSCommand(ws, "run-task", "")
	if err != nil {
		return "", err
	}
	id := command["__task_id"].(string)
	if err := sendTTSControl(ws, "task-started", id, ""); err != nil {
		return "", err
	}
	if _, err := readTTSCommand(ws, "continue-task", id); err != nil {
		return "", err
	}
	if _, err := readTTSCommand(ws, "finish-task", id); err != nil {
		return "", err
	}
	return id, nil
}

func TestCosyVoiceEmptyBatchIsProtocolFailure(t *testing.T) {
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
	_, err = client.Synthesize(ctx, testRequest())
	if !speecherr.IsCode(err, speecherr.CodeProtocol) {
		t.Fatalf("empty synthesis must fail with CodeProtocol: %v", err)
	}
}

func TestCosyVoiceRecvDeadlineRetainsOriginalCause(t *testing.T) {
	freed := make(chan bool, 1)
	endpoint := mockTTS(t, func(ws *websocket.Conn, _ *http.Request) {
		_, err := mockReady(ws)
		if err != nil {
			freed <- false
			return
		}
		_, _, err = ws.ReadMessage()
		freed <- err != nil
	})
	client, err := New(Config{APIKey: "test", Endpoint: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	parent, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s, err := client.SynthesizeStream(parent, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	callCtx, stop := context.WithTimeout(context.Background(), 70*time.Millisecond)
	_, first := s.Recv(callCtx)
	stop()
	if !errors.Is(first, context.DeadlineExceeded) {
		t.Fatalf("Recv deadline lost: %v", first)
	}
	for i := 0; i < 2; i++ {
		_, next := s.Recv(context.Background())
		if !errors.Is(next, context.DeadlineExceeded) {
			t.Fatalf("Recv %d lost original deadline: %v", i, next)
		}
	}
	select {
	case closed := <-freed:
		if !closed {
			t.Fatal("peer did not close after deadline")
		}
	case <-time.After(2*time.Second):
		t.Fatal("peer blocked after stream deadline")
	}
}

func TestCosyVoiceExplicitCloseWithoutRecvReleasesPeer(t *testing.T) {
	freed := make(chan bool, 1)
	endpoint := mockTTS(t, func(ws *websocket.Conn, _ *http.Request) {
		_, err := mockReady(ws)
		if err != nil {
			freed <- false
			return
		}
		_, _, err = ws.ReadMessage()
		freed <- err != nil
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
	for i := 0; i < 2; i++ {
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	_, err = s.Recv(context.Background())
	if !errors.Is(err, errClosed) || errors.Is(err, io.EOF) {
		t.Fatalf("unexpected Recv after explicit close: %v", err)
	}
	select {
	case closed := <-freed:
		if !closed {
			t.Fatal("peer not released by Close")
		}
	case <-time.After(2*time.Second):
		t.Fatal("peer still blocked after Close")
	}
}

func TestCosyVoiceMismatchedTaskIDIsProtocolFailure(t *testing.T) {
	endpoint := mockTTS(t, func(ws *websocket.Conn, _ *http.Request) {
		id, err := mockReady(ws)
		if err == nil {
			_ = sendTTSControl(ws, "task-finished", id+"-wrong", "")
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
		t.Fatalf("unexpected task_id did not fail the protocol: %v", first)
	}
	_, second := s.Recv(context.Background())
	if first != second {
		t.Fatalf("terminal error changed after mismatch: %v vs %v", first, second)
	}
}

func TestCosyVoiceStartupTaskFailed(t *testing.T) {
	endpoint := mockTTS(t, func(ws *websocket.Conn, _ *http.Request) {
		req, err := readTTSCommand(ws, "run-task", "")
		if err != nil {
			return
		}
		_ = sendTTSControl(ws, "task-failed", req["__task_id"].(string), "Throttling")
	})
	client, err := New(Config{APIKey: "test", Endpoint: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = client.SynthesizeStream(ctx, testRequest())
	if !speecherr.IsCode(err, speecherr.CodeRateLimited) {
		t.Fatalf("task-start failure lost classification: %v", err)
	}
}
