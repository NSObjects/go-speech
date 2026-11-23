package aliyun

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Reusing one client must not retain sockets between completed requests.
func TestCosyVoiceRepeatedBatchAndStreamReleaseConnections(t *testing.T) {
	const rounds = 12
	var active atomic.Int32
	released := make(chan struct{}, rounds*2)
	endpoint := mockTTS(t, func(ws *websocket.Conn, _ *http.Request) {
		active.Add(1)
		defer func() {
			active.Add(-1)
			released <- struct{}{}
		}()
		id, err := mockReady(ws)
		if err != nil {
			return
		}
		if err := ws.WriteMessage(websocket.BinaryMessage, []byte{1, 2, 3, 4}); err != nil {
			return
		}
		_ = sendTTSControl(ws, "task-finished", id, "")
		_, _, _ = ws.ReadMessage() // Close must release the peer.
	})
	client, err := New(Config{APIKey: "test", Endpoint: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for i := 0; i < rounds; i++ {
		if i%2 == 0 {
			out, err := client.Synthesize(ctx, testRequest())
			if err != nil || string(out.Data) != string([]byte{1, 2, 3, 4}) {
				t.Fatalf("batch iteration %d: bytes=%v error=%v", i, out.Data, err)
			}
			continue
		}
		stream, err := client.SynthesizeStream(ctx, testRequest())
		if err != nil {
			t.Fatalf("stream iteration %d start: %v", i, err)
		}
		chunk, err := stream.Recv(ctx)
		if err != nil || string(chunk.Data) != string([]byte{1, 2, 3, 4}) {
			t.Fatalf("stream iteration %d audio: bytes=%v error=%v", i, chunk.Data, err)
		}
		if _, err := stream.Recv(ctx); !errors.Is(err, io.EOF) {
			t.Fatalf("stream iteration %d final: %v", i, err)
		}
		if err := stream.Close(); err != nil {
			t.Fatalf("stream iteration %d close: %v", i, err)
		}
	}
	for i := 0; i < rounds; i++ {
		select {
		case <-released:
		case <-ctx.Done():
			t.Fatalf("only %d/%d connections released: %v", i, rounds, ctx.Err())
		}
	}
	if n := active.Load(); n != 0 {
		t.Fatalf("connections remain active: %d", n)
	}
}
