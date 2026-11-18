package volcengine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/gorilla/websocket"
)

// Explicit Close must release an idle WebSocket even if the caller never
// starts Recv or CloseSend. The peer's blocked read must become unblocked.
func TestExplicitCloseWithoutRecvReleasesSocket(t *testing.T) {
	peerClosed := make(chan error, 1)
	endpoint := mockEndpoint(t, func(ws *websocket.Conn, _ *http.Request) {
		if _, _, err := ws.ReadMessage(); err != nil {
			peerClosed <- fmt.Errorf("read initial config: %w", err)
			return
		}
		if _, _, err := ws.ReadMessage(); err == nil {
			peerClosed <- errors.New("server read unexpectedly completed without client audio")
		} else {
			peerClosed <- nil
		}
	})
	client, err := New(Config{APIKey: "test-key", Endpoint: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := client.OpenStream(ctx, asr.Config{
		Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("repeated Close: %v", err)
	}
	if err := s.Send(ctx, []byte{0, 0}); !errors.Is(err, asr.ErrStreamClosed) {
		t.Fatalf("Send after Close: %v", err)
	}
	if _, err := s.Recv(ctx); !errors.Is(err, asr.ErrStreamClosed) {
		t.Fatalf("Recv after Close: %v", err)
	}
	select {
	case err := <-peerClosed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("peer socket still blocked after client.Close")
	}
}

// Cancel one session during its active audio stream. The shared Client must
// continue serving the other session, and the canceled socket must be freed.
func TestCancelOneStreamDoesNotAffectSibling(t *testing.T) {
	peerResult := make(chan error, 2)
	endpoint := mockEndpoint(t, func(ws *websocket.Conn, _ *http.Request) {
		if _, _, err := ws.ReadMessage(); err != nil {
			peerResult <- fmt.Errorf("read initial config: %w", err)
			return
		}
		_, frame, err := ws.ReadMessage()
		if err != nil {
			peerResult <- fmt.Errorf("read audio: %w", err)
			return
		}
		kind, _, audioData, err := testRequestFrame(frame)
		if err != nil || kind != messageAudioRequest || len(audioData) != 2 {
			peerResult <- fmt.Errorf("invalid initial audio frame: %v, %d, %d", err, kind, len(audioData))
			return
		}
		switch audioData[0] {
		case 1:
			if _, _, err := ws.ReadMessage(); err == nil {
				peerResult <- errors.New("canceled stream socket remained open")
			} else {
				peerResult <- nil
			}
		case 2:
			_, finalFrame, err := ws.ReadMessage()
			if err != nil {
				peerResult <- fmt.Errorf("read sibling CloseSend: %w", err)
				return
			}
			kind, flags, data, err := testRequestFrame(finalFrame)
			if err != nil || kind != messageAudioRequest || flags != flagLast || len(data) != 0 {
				peerResult <- fmt.Errorf("invalid sibling CloseSend: %v, %d, %d", err, kind, flags)
				return
			}
			serverResult(t, ws, true, map[string]any{
				"code": 1000,
				"result": map[string]any{"utterances": []any{
					map[string]any{"text": "sibling", "definite": true, "start_time": 0, "end_time": 500},
				}},
			})
			peerResult <- nil
		default:
			peerResult <- fmt.Errorf("unexpected session marker %d", audioData[0])
		}
	})
	client, err := New(Config{APIKey: "test-key", Endpoint: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	session1, err := client.OpenStream(ctx1, asr.Config{
		Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session1.Close()
	if err := session1.Send(ctx1, []byte{1, 0}); err != nil {
		t.Fatal(err)
	}
	cancel1()
	if _, err := session1.Recv(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled stream lost context error: %v", err)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	session2, err := client.OpenStream(ctx2, asr.Config{
		Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session2.Close()
	if err := session2.Send(ctx2, []byte{2, 0}); err != nil {
		t.Fatal(err)
	}
	if err := session2.CloseSend(ctx2); err != nil {
		t.Fatal(err)
	}
	event, err := session2.Recv(ctx2)
	if err != nil || event.Type != asr.EventFinal || event.Text != "sibling" {
		t.Fatalf("sibling received wrong final: %#v, %v", event, err)
	}
	if _, err := session2.Recv(ctx2); !errors.Is(err, io.EOF) {
		t.Fatalf("sibling EOF: %v", err)
	}
	for i := 0; i < 2; i++ {
		select {
		case err := <-peerResult:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("peer socket did not release or sibling did not complete")
		}
	}
}
