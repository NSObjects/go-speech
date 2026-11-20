package tencent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/speecherr"
	"github.com/gorilla/websocket"
)

// waitForTencentHandshakePeerRelease checks that OpenStream does not leave an
// upgraded WebSocket or its server goroutine running after startup fails.
func waitForTencentHandshakePeerRelease(t *testing.T, released <-chan error) {
	t.Helper()
	select {
	case err := <-released:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("OpenStream failed but left the handshake WebSocket open")
	}
}

func receiveTencentHandshakePeerClose(ws *websocket.Conn) error {
	// Keep the test bounded even if a regression leaves the socket open.
	if err := ws.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return err
	}
	_, _, err := ws.ReadMessage()
	if err == nil {
		return errors.New("unexpected audio received after handshake failed")
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return fmt.Errorf("WebSocket was not closed after handshake failure: %w", err)
	}
	return nil
}

// Tencent V2's first response is a handshake ACK. Recognition data in that
// first frame must never be silently consumed by OpenStream.
func TestTencentV2EarlySentenceDuringHandshakeFailsAndReleasesSocket(t *testing.T) {
	released := make(chan error, 1)
	client := testClient(t, func(ws *websocket.Conn, req *http.Request) {
		id := req.URL.Query().Get("voice_id")
		raw, err := json.Marshal(map[string]any{
			"code": 0, "voice_id": id, "final": 0,
			"sentences": map[string]any{
				"sentence_list": []map[string]any{{
					"sentence_id": 0, "sentence_type": 0,
					"sentence": "unexpected transcript in handshake",
				}},
			},
		})
		if err != nil {
			released <- err
			return
		}
		if err := ws.WriteMessage(websocket.TextMessage, raw); err != nil {
			released <- err
			return
		}
		released <- receiveTencentHandshakePeerClose(ws)
	})
	client.cfg.EngineModel = "16k_zh_en_2.0"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.OpenStream(ctx, pcmConfig())
	if stream != nil {
		_ = stream.Close()
		t.Fatal("OpenStream accepted a recognition result as handshake ACK")
	}
	if !speecherr.IsCode(err, speecherr.CodeProtocol) {
		t.Fatalf("V2 result in handshake = %v; want CodeProtocol", err)
	}
	waitForTencentHandshakePeerRelease(t, released)
}

func TestTencentHandshakeCancellationAndTimeoutReleaseSocket(t *testing.T) {
	for _, mode := range []string{"cancel", "startup_timeout"} {
		t.Run(mode, func(t *testing.T) {
			ready := make(chan struct{}, 1)
			released := make(chan error, 1)
			client := testClient(t, func(ws *websocket.Conn, req *http.Request) {
				ready <- struct{}{}
				// Deliberately do not ACK. The SDK must abort a pending
				// ReadMessage and release the peer-side socket.
				released <- receiveTencentHandshakePeerClose(ws)
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if mode == "startup_timeout" {
				client.cfg.StartTimeout = 750 * time.Millisecond
			}
			type result struct {
				streamOpen bool
				err error
			}
			resultCh := make(chan result, 1)
			go func() {
				s, err := client.OpenStream(ctx, pcmConfig())
				if s != nil {
					_ = s.Close()
				}
				resultCh <- result{streamOpen: s != nil, err: err}
			}()
			select {
			case <-ready:
				if mode == "cancel" {
					cancel()
				}
			case <-time.After(3 * time.Second):
				t.Fatal("mock was not reached during WebSocket handshake")
			}
			select {
			case got := <-resultCh:
				if got.streamOpen {
					t.Fatal("failed/canceled handshake returned a live stream")
				}
				if mode == "cancel" {
					if !errors.Is(got.err, context.Canceled) {
						t.Fatalf("canceled handshake = %v, want context.Canceled", got.err)
					}
				} else if !speecherr.IsCode(got.err, speecherr.CodeTimeout) {
					t.Fatalf("stalled handshake = %v, want CodeTimeout", got.err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("OpenStream did not exit after handshake cancellation/timeout")
			}
			waitForTencentHandshakePeerRelease(t, released)
		})
	}
}
