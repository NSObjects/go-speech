package tencent

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/gorilla/websocket"
)

// A late provisional update cannot overwrite text already finalized for
// the same sentence. This can happen when buffered frames are delivered
// out of order across a reconnecting network.
func TestTencentIgnoresLatePartialsAfterFinal(t *testing.T) {
	for _, tc := range []struct {
		name string
		v2   bool
	}{
		{name: "legacy"},
		{name: "v2 sentence list", v2: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			serverErrors := make(chan error, 1)
			client := testClient(t, func(ws *websocket.Conn, req *http.Request) {
				id := req.URL.Query().Get("voice_id")
				if err := sendResponse(ws, id, 0, 0, nil); err != nil {
					serverErrors <- err
					return
				}
				if _, _, err := ws.ReadMessage(); err != nil {
					serverErrors <- err
					return
				}
				if tc.v2 {
					for _, sentences := range [][]map[string]any{
						{{"sentence_id": 0, "sentence_type": 1, "sentence": "已确认"}},
						{
							{"sentence_id": 0, "sentence_type": 0, "sentence": "过期中间结果"},
							{"sentence_id": 0, "sentence_type": 1, "sentence": "重复定稿"},
							{"sentence_id": 1, "sentence_type": 1, "sentence": "第二句"},
						},
					} {
						if err := ws.WriteJSON(map[string]any{
							"code": 0, "voice_id": id,
							"sentences": map[string]any{"sentence_list": sentences},
						}); err != nil {
							serverErrors <- err
							return
						}
					}
				} else {
					for _, r := range []*segment{
						{SliceType: 2, Index: 0, Text: "已确认"},
						{SliceType: 1, Index: 0, Text: "过期中间结果"},
						{SliceType: 2, Index: 0, Text: "重复定稿"},
						{SliceType: 2, Index: 1, Text: "第二句"},
					} {
						if err := sendResponse(ws, id, 0, 0, r); err != nil {
							serverErrors <- err
							return
						}
					}
				}
				if err := sendResponse(ws, id, 0, 1, nil); err != nil {
					serverErrors <- err
					return
				}
				serverErrors <- nil
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			s, err := client.OpenStream(ctx, pcmConfig())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err := s.Send(ctx, []byte{0, 0}); err != nil {
				t.Fatal(err)
			}
			for i, expected := range []string{"已确认", "第二句"} {
				event, err := s.Recv(ctx)
				if err != nil || event.Type != asr.EventFinal || event.Text != expected {
					t.Fatalf("result %d = %+v / %v, want final %q", i, event, err, expected)
				}
			}
			for i := 0; i < 2; i++ {
				if _, err := s.Recv(ctx); !errors.Is(err, io.EOF) {
					t.Fatalf("terminal EOF %d: %v", i, err)
				}
			}
			if err := <-serverErrors; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTencentAbruptDisconnectRetainsOriginalFailure(t *testing.T) {
	client := testClient(t, func(ws *websocket.Conn, req *http.Request) {
		if err := sendResponse(ws, req.URL.Query().Get("voice_id"), 0, 0, nil); err != nil {
			return
		}
		if _, _, err := ws.ReadMessage(); err != nil {
			return
		}
		// Close the underlying TCP socket without a WebSocket close handshake.
		_ = ws.UnderlyingConn().Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := client.OpenStream(ctx, pcmConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Send(ctx, []byte{0, 0}); err != nil {
		t.Fatal(err)
	}
	_, first := s.Recv(ctx)
	if !speecherr.IsCode(first, speecherr.CodeUnavailable) {
		t.Fatalf("abrupt peer disconnect should be unavailable: %v", first)
	}
	for i := 0; i < 3; i++ {
		_, again := s.Recv(context.Background())
		if again != first {
			t.Fatalf("terminal failure was replaced: first=%v repeat=%v", first, again)
		}
	}
	if err := s.Send(ctx, []byte{0, 0}); !errors.Is(err, asr.ErrStreamClosed) {
		t.Fatalf("send after failed stream: %v", err)
	}
}
