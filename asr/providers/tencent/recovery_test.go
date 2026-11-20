package tencent

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/gorilla/websocket"
)

func TestTencentRemoteRetryAdvice(t *testing.T) {
	for _, tc := range []struct {
		name      string
		remote    int
		want      speecherr.Code
		retryable bool
	}{
		{name: "quota exhausted", remote: 4004, want: speecherr.CodeRateLimited},
		{name: "concurrent limit", remote: 4006, want: speecherr.CodeRateLimited, retryable: true},
		{name: "bad signature", remote: 4002, want: speecherr.CodeUnauthorized},
		{name: "bad audio pacing", remote: 4000, want: speecherr.CodeInvalidArgument},
		{name: "client idle", remote: 4008, want: speecherr.CodeTimeout},
		{name: "connection dropped", remote: 4009, want: speecherr.CodeUnavailable},
		{name: "server overloaded", remote: 5000, want: speecherr.CodeUnavailable, retryable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := remoteError(response{Code: tc.remote, Message: tc.name})
			var classified *speecherr.Error
			if !errors.As(err, &classified) {
				t.Fatalf("want typed Tencent failure, got %v", err)
			}
			if classified.Code != tc.want || classified.Retryable != tc.retryable {
				t.Fatalf("remote %d classified as code=%s retryable=%t, want %s/%t",
					tc.remote, classified.Code, classified.Retryable, tc.want, tc.retryable)
			}
			if classified.RemoteCode == "" {
				t.Fatal("Tencent remote code should remain available to the caller")
			}
		})
	}
}

// A recoverable socket error is not an instruction to replay buffered PCM or
// finalized text. The caller starts a brand new session with a new voice_id.
// Only the caller decides which NEW audio, if any, belongs to that session.
func TestTencentRecoveryStartsFreshSessionWithoutReplay(t *testing.T) {
	var opened atomic.Int32
	var newSessionAudioRequested atomic.Bool
	var mu sync.Mutex
	voiceIDs := make([]string, 0, 2)
	received := make([][]byte, 0, 2)
	beforeExplicitSend := make(chan bool, 1)
	client := testClient(t, func(ws *websocket.Conn, req *http.Request) {
		number := int(opened.Add(1))
		id := req.URL.Query().Get("voice_id")
		mu.Lock()
		voiceIDs = append(voiceIDs, id)
		mu.Unlock()
		if err := sendResponse(ws, id, 0, 0, nil); err != nil {
			t.Errorf("handshake %d: %v", number, err)
			return
		}
		typ, data, err := ws.ReadMessage()
		if err != nil {
			t.Errorf("read audio %d: %v", number, err)
			return
		}
		if typ != websocket.BinaryMessage {
			t.Errorf("session %d unexpected audio type %d", number, typ)
			return
		}
		if number == 2 {
			beforeExplicitSend <- !newSessionAudioRequested.Load()
		}
		mu.Lock()
		received = append(received, append([]byte(nil), data...))
		mu.Unlock()
		if number == 1 {
			if err := sendResponse(ws, id, 0, 0, &segment{
				SliceType: 2, Index: 0, Text: "旧会话结果",
			}); err != nil {
				t.Errorf("first final: %v", err)
				return
			}
			// Simulate a network failure after a final segment, but before
			// Tencent's session-level final=1 acknowledgement.
			_ = ws.UnderlyingConn().Close()
			return
		}
		if _, _, err := ws.ReadMessage(); err != nil {
			t.Errorf("end message: %v", err)
			return
		}
		if err := sendResponse(ws, id, 0, 1, &segment{
			SliceType: 2, Index: 0, Text: "新会话结果",
		}); err != nil {
			t.Errorf("second final: %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, err := client.OpenStream(ctx, pcmConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := first.Send(ctx, []byte{1, 0}); err != nil {
		t.Fatal(err)
	}
	got, err := first.Recv(ctx)
	if err != nil || got.Type != asr.EventFinal || got.Text != "旧会话结果" {
		t.Fatalf("first final = %+v / %v", got, err)
	}
	_, firstErr := first.Recv(ctx)
	if !speecherr.IsCode(firstErr, speecherr.CodeUnavailable) {
		t.Fatalf("socket failure following a final must be reported: %v", firstErr)
	}

	second, err := client.OpenStream(ctx, pcmConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	// Give any hypothetical SDK-level automatic replay a chance to occur.
	time.Sleep(40 * time.Millisecond)
	newSessionAudioRequested.Store(true)
	if err := second.Send(ctx, []byte{2, 0}); err != nil {
		t.Fatal(err)
	}
	if err := second.CloseSend(ctx); err != nil {
		t.Fatal(err)
	}
	got, err = second.Recv(ctx)
	if err != nil || got.Type != asr.EventFinal || got.Text != "新会话结果" {
		t.Fatalf("new session final = %+v / %v", got, err)
	}
	if _, err := second.Recv(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("new session must end with EOF: %v", err)
	}
	select {
	case autoReplayed := <-beforeExplicitSend:
		if autoReplayed {
			t.Fatal("SDK replayed audio on the replacement WebSocket before caller Send")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	mu.Lock()
	defer mu.Unlock()
	if len(voiceIDs) != 2 || voiceIDs[0] == "" || voiceIDs[0] == voiceIDs[1] {
		t.Fatalf("fresh Tencent stream needs distinct voice IDs (count=%d)", len(voiceIDs))
	}
	if len(received) != 2 || len(received[0]) != 2 || received[0][0] != 1 ||
		len(received[1]) != 2 || received[1][0] != 2 {
		t.Fatalf("replacement stream must only carry caller-supplied fresh PCM (received=%d)", len(received))
	}
}
