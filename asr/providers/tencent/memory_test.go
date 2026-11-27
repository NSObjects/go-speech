package tencent

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/gorilla/websocket"
)

func TestCloseReleasesRecognitionHistory(t *testing.T) {
	for _, final := range []bool{false, true} {
		c := testClient(t, func(ws *websocket.Conn, req *http.Request) {
			id := req.URL.Query().Get("voice_id")
			if err := sendResponse(ws, id, 0, 0, nil); err != nil {
				return
			}
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
			last := 0
			if final {
				last = 1
			}
			if err := ws.WriteJSON(map[string]any{
				"code": 0, "voice_id": id, "final": last,
				"sentences": map[string]any{"sentence_list": []map[string]any{
					{"sentence": "first", "sentence_type": 1, "sentence_id": 0},
					{"sentence": "second", "sentence_type": 1, "sentence_id": 1},
				}},
			}); err != nil {
				t.Error(err)
			}
			if !final {
				_, _, _ = ws.ReadMessage()
			}
		})
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		stream, err := c.OpenStream(ctx, pcmConfig())
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		s := stream.(*Stream)
		if err := s.Send(ctx, []byte{0, 0}); err != nil {
			cancel()
			t.Fatal(err)
		}
		e, err := s.Recv(ctx)
		if err != nil || e.Text != "first" {
			cancel()
			t.Fatalf("first event: %+v, %v", e, err)
		}
		_ = s.Close()
		s.readMu.Lock()
		historyReleased := s.finalSeen == nil
		s.readMu.Unlock()
		if !historyReleased {
			cancel()
			t.Fatal("closed session retained dedup history")
		}
		if final {
			late := errors.New("late write failure")
			if err := s.failWrite(late); err != late {
				t.Fatalf("sender error = %v", err)
			}
			cancel()
			if err := s.terminalFailure(); err != nil {
				t.Fatalf("completed stream overwritten: %v", err)
			}
			if e, err := s.Recv(ctx); err != nil || e.Text != "second" {
				cancel()
				t.Fatalf("final backlog lost: %+v, %v", e, err)
			}
			if _, err := s.Recv(ctx); !errors.Is(err, io.EOF) {
				cancel()
				t.Fatalf("expected EOF: %v", err)
			}
		} else if _, err := s.Recv(ctx); !errors.Is(err, asr.ErrStreamClosed) {
			cancel()
			t.Fatalf("active backlog was not discarded: %v", err)
		}
		s.readMu.Lock()
		queueReleased := s.queue == nil
		s.readMu.Unlock()
		cancel()
		if !queueReleased {
			t.Fatal("closed session retained queue")
		}
	}
}
