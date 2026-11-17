package volcengine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/gorilla/websocket"
)

func TestEndWindowSizeValidation(t *testing.T) {
	for _, window := range []time.Duration{
		-time.Millisecond, time.Nanosecond, 199 * time.Millisecond,
		700*time.Millisecond + time.Microsecond,
	} {
		if _, err := New(Config{APIKey: "key", EndWindowSize: window}); !speecherr.IsCode(err, speecherr.CodeInvalidArgument) {
			t.Errorf("EndWindowSize=%s: want invalid argument, got %v", window, err)
		}
	}
	for _, window := range []time.Duration{0, 200 * time.Millisecond, 700 * time.Millisecond} {
		if _, err := New(Config{APIKey: "key", EndWindowSize: window}); err != nil {
			t.Errorf("EndWindowSize=%s: unexpected %v", window, err)
		}
	}
}

func TestEndWindowSizeWireRequest(t *testing.T) {
	for _, tc := range []struct {
		name   string
		window time.Duration
		wantMS int64
	}{
		{name: "default omitted"},
		{name: "custom 700ms", window: 700 * time.Millisecond, wantMS: 700},
		{name: "minimum 200ms", window: 200 * time.Millisecond, wantMS: 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			received := make(chan error, 1)
			endpoint := mockEndpoint(t, func(ws *websocket.Conn, _ *http.Request) {
				typ, frame, err := ws.ReadMessage()
				if err != nil || typ != websocket.BinaryMessage {
					received <- fmt.Errorf("read request: type=%d err=%v", typ, err)
					return
				}
				kind, _, payload, err := testRequestFrame(frame)
				if err != nil || kind != messageFullRequest {
					received <- fmt.Errorf("invalid request: kind=%d err=%v", kind, err)
					return
				}
				var request struct {
					Request map[string]json.RawMessage `json:"request"`
				}
				if err := json.Unmarshal(payload, &request); err != nil {
					received <- err
					return
				}
				raw, present := request.Request["end_window_size"]
				if tc.window == 0 {
					if present {
						received <- fmt.Errorf("default must omit end_window_size: %s", raw)
					} else {
						received <- nil
					}
					return
				}
				if !present {
					received <- fmt.Errorf("end_window_size missing for %s", tc.window)
					return
				}
				var got int64
				if err := json.Unmarshal(raw, &got); err != nil {
					received <- err
				} else if got != tc.wantMS {
					received <- fmt.Errorf("end_window_size=%d, want %d", got, tc.wantMS)
				} else {
					received <- nil
				}
			})
			client, err := New(Config{APIKey: "key", Endpoint: endpoint, EndWindowSize: tc.window})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			stream, err := client.OpenStream(ctx, asr.Config{
				Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			select {
			case err := <-received:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		})
	}
}
