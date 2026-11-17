package volcengine

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/gorilla/websocket"
)

func TestNoSpeechResponseDoesNotCommitProvisionalText(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind byte
	}{
		{name: "status in full response", kind: messageFullResponse},
		{name: "status in error frame", kind: messageServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := mockEndpoint(t, func(ws *websocket.Conn, _ *http.Request) {
				_, _, _ = ws.ReadMessage() // initial configuration
				serverResult(t, ws, false, map[string]any{
					"code": 1000,
					"result": map[string]any{
						"text":       "ghost",
						"utterances": []any{map[string]any{"text": "ghost", "definite": false}},
					},
				})
				switch tc.kind {
				case messageFullResponse:
					// In practice 1013 can be sent without an explicit last flag.
					serverResult(t, ws, false, map[string]any{"code": 1013, "message": "silent audio"})
				case messageServerError:
					frame, err := encodeFrame(messageServerError, 0, 1, []byte(`{"message":"silent audio"}`))
					if err != nil {
						t.Error(err)
						return
					}
					// Server errors include a four-byte code before the length.
					// Build that extension separately from the standard framed payload.
					extended := make([]byte, 4+4+len(frame)-4)
					copy(extended[:4], frame[:4])
					extended[4], extended[5], extended[6], extended[7] = 0, 0, 3, 245 // 1013
					copy(extended[8:], frame[4:])
					if err := ws.WriteMessage(websocket.BinaryMessage, extended); err != nil {
						t.Error(err)
					}
				}
			})
			client, err := New(Config{APIKey: "test-key", Endpoint: endpoint})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			stream, err := client.OpenStream(ctx, asr.Config{
				Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			ev, err := stream.Recv(ctx)
			if err != nil || ev != (asr.Event{Type: asr.EventPartial, Text: "ghost"}) {
				t.Fatalf("initial partial: %#v %v", ev, err)
			}
			for i := 0; i < 2; i++ {
				ev, err = stream.Recv(ctx)
				if !errors.Is(err, io.EOF) {
					t.Fatalf("after no-speech response %d: got %#v, %v; want EOF", i, ev, err)
				}
			}
		})
	}
}

func TestSilenceWithCloseSendAndFinalNoSpeech(t *testing.T) {
	endpoint := mockEndpoint(t, func(ws *websocket.Conn, _ *http.Request) {
		for i := 0; i < 3; i++ { // initial config, silent PCM, final marker
			if _, _, err := ws.ReadMessage(); err != nil {
				t.Error(err)
				return
			}
		}
		serverResult(t, ws, true, map[string]any{"code": 1013})
	})
	client, err := New(Config{APIKey: "key", Endpoint: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.OpenStream(ctx, asr.Config{
		Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err := stream.Send(ctx, make([]byte, 3200)); err != nil {
		t.Fatal(err)
	}
	if err := stream.CloseSend(ctx); err != nil {
		t.Fatal(err)
	}
	if ev, err := stream.Recv(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("silent result: %#v, %v", ev, err)
	}
}

func TestDefiniteSupersedesEarlierProvisionalInSamePacket(t *testing.T) {
	for _, last := range []bool{false, true} {
		s := &Stream{resultType: ResultFull, finalSeen: make(map[string]struct{})}
		s.addEvents([]recognition{{Utterances: []utterance{
			{Text: "hello", Definite: false, Start: 0, End: 250},
			{Text: "hello world", Definite: true, Start: 0, End: 500},
		}}}, last)
		want := []asr.Event{{Type: asr.EventFinal, Text: "hello world"}}
		if !reflect.DeepEqual(s.queue, want) {
			t.Fatalf("last=%v, emitted %#v; want %#v", last, s.queue, want)
		}
		if s.pendingText != "" {
			t.Fatalf("last=%v has stale provisional %q", last, s.pendingText)
		}
	}
}

func TestEmptyTerminalEnvelopeRetainsLatestProvisional(t *testing.T) {
	s := &Stream{resultType: ResultSingle, finalSeen: make(map[string]struct{})}
	s.addEvents([]recognition{{Text: "hello"}}, false)
	s.queue = nil
	s.addEvents(nil, true)
	want := []asr.Event{{Type: asr.EventFinal, Text: "hello"}}
	if !reflect.DeepEqual(s.queue, want) {
		t.Fatalf("empty terminal envelope dropped provisional: %#v", s.queue)
	}
}

func TestAbruptWebSocketDisconnectPreservesFirstError(t *testing.T) {
	endpoint := mockEndpoint(t, func(ws *websocket.Conn, _ *http.Request) {
		_, _, _ = ws.ReadMessage()
		serverResult(t, ws, false, map[string]any{
			"code": 1000, "result": map[string]any{"text": "hello"},
		})
		_ = ws.UnderlyingConn().Close() // no protocol final message
	})
	client, err := New(Config{APIKey: "key", Endpoint: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.OpenStream(ctx, asr.Config{
		Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if ev, err := stream.Recv(ctx); err != nil || ev.Type != asr.EventPartial || ev.Text != "hello" {
		t.Fatalf("first partial: %#v, %v", ev, err)
	}
	_, first := stream.Recv(ctx)
	if !speecherr.IsCode(first, speecherr.CodeUnavailable) {
		t.Fatalf("unexpected disconnect classification: %v", first)
	}
	_, second := stream.Recv(context.Background())
	if second != first {
		t.Fatalf("terminal error changed: %v vs %v", first, second)
	}
	if err := stream.Send(ctx, []byte{0, 0}); !errors.Is(err, asr.ErrStreamClosed) {
		t.Fatalf("send on failed stream: %v", err)
	}
}
