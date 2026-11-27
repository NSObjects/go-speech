package tencent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/speecherr"
	"github.com/gorilla/websocket"
)

func TestTencentRejectsMissingOrNullResponseCode(t *testing.T) {
	for _, raw := range []string{
		`{"voice_id":"expected","final":0}`,
		`{"voice_id":"expected","code":null,"final":0}`,
		`{"voice_id":"expected","code":"0","final":0}`,
	} {
		_, err := decodeResponse([]byte(raw), "expected", false)
		if !speecherr.IsCode(err, speecherr.CodeProtocol) {
			t.Fatalf("response %s: expected protocol failure, got %v", raw, err)
		}
	}
	for _, raw := range []string{
		`{"voice_id":"expected","code":0,"final":0}`,
		`{"voice_id":"expected","code":4002,"message":"unauthorized"}`,
	} {
		_, err := decodeResponse([]byte(raw), "expected", false)
		if speecherr.IsCode(err, speecherr.CodeProtocol) {
			t.Fatalf("response %s: unexpectedly rejected valid numeric code: %v", raw, err)
		}
	}
}

func TestTencentMissingStatusFailsHandshakeAndActiveRecv(t *testing.T) {
	for _, phase := range []string{"handshake", "recv"} {
		t.Run(phase, func(t *testing.T) {
			client := testClient(t, func(ws *websocket.Conn, req *http.Request) {
				id := req.URL.Query().Get("voice_id")
				if phase == "recv" {
					if err := sendResponse(ws, id, 0, 0, nil); err != nil {
						return
					}
					if _, _, err := ws.ReadMessage(); err != nil {
						return
					}
				}
				payload, err := json.Marshal(map[string]any{
					"voice_id": id, "final": 1,
				})
				if err != nil {
					t.Errorf("marshal response: %v", err)
					return
				}
				_ = ws.WriteMessage(websocket.TextMessage, payload)
			})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			stream, err := client.OpenStream(ctx, pcmConfig())
			if phase == "handshake" {
				if stream != nil {
					_ = stream.Close()
					t.Fatal("handshake accepted a response without a status code")
				}
				if !speecherr.IsCode(err, speecherr.CodeProtocol) {
					t.Fatalf("handshake = %v; want CodeProtocol", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			if err := stream.Send(ctx, []byte{1, 0}); err != nil {
				t.Fatal(err)
			}
			_, first := stream.Recv(ctx)
			if !speecherr.IsCode(first, speecherr.CodeProtocol) {
				t.Fatalf("Recv accepted final without code: %v", first)
			}
			_, second := stream.Recv(context.Background())
			if !errors.Is(second, first) {
				t.Fatalf("protocol error was lost: first=%v second=%v", first, second)
			}
		})
	}
}

func TestDecodeResponseRawMetadataOptIn(t *testing.T) {
	const payload = `{"code":0,"voice_id":"expected","result":{"slice_type":2,"index":4,"voice_text_str":"你好","unknown_field":{"emotion":"happy"},"word_list":[{"word":"你好","start_time":0,"end_time":200}]}}`
	for _, details := range []bool{false, true} {
		raw := []byte(payload)
		r, err := decodeResponse(raw, "expected", details)
		if err != nil || r.Result == nil || r.Result.Text != "你好" || len(r.Result.Words) != 1 || r.Result.Index != 4 {
			t.Fatalf("details=%t: result=%+v err=%v", details, r, err)
		}
		if !details {
			if r.Result.raw != nil {
				t.Fatal("plain recognition copied native result")
			}
			continue
		}
		clear(raw)
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(r.Result.raw, &fields); err != nil || string(fields["unknown_field"]) != `{"emotion":"happy"}` {
			t.Fatalf("native result was not independently preserved: %s (%v)", r.Result.raw, err)
		}
	}
	for _, raw := range []string{
		`{"voice_id":"expected"}`, `{"code":null,"voice_id":"expected"}`, `{"code":"0","voice_id":"expected"}`,
	} {
		if _, err := decodeResponse([]byte(raw), "expected", true); !speecherr.IsCode(err, speecherr.CodeProtocol) {
			t.Fatalf("detailed decoder accepted invalid status: %v", err)
		}
	}
}

func BenchmarkDecodeResponse(b *testing.B) {
	raw := []byte(`{"code":0,"voice_id":"expected","result":{"slice_type":2,"index":4,"voice_text_str":"你好","word_list":[{"word":"你好","start_time":0,"end_time":200}],"future_metadata":{"label":"happy"}}}`)
	for _, tc := range []struct {
		name    string
		details bool
	}{
		{"plain", false}, {"detailed", true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := decodeResponse(raw, "expected", tc.details); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
