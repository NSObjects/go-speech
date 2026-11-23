package volcengine

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/speecherr"
	"github.com/gorilla/websocket"
)

func TestVolcengineEmptyStreamIsStableProtocolFailure(t *testing.T) {
	endpoint := mockTTS(t, func(ws *websocket.Conn, _ *http.Request) {
		if _, _, err := ws.ReadMessage(); err != nil {
			return
		}
		_ = ws.WriteMessage(websocket.BinaryMessage,
			responseFrame(kindFullResponse, eventSessionFinished, "session", []byte(`{"usage":{"text_words":2}}`)))
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
		t.Fatalf("empty stream accepted as success: %v", first)
	}
	_, again := s.Recv(context.Background())
	if first != again {
		t.Fatalf("terminal protocol error changed: %v, %v", first, again)
	}
}
