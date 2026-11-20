package volcengine

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/gorilla/websocket"
)

type injectedDialTimeout struct{}

func (injectedDialTimeout) Error() string   { return "injected WebSocket handshake timeout" }
func (injectedDialTimeout) Timeout() bool   { return true }
func (injectedDialTimeout) Temporary() bool { return true }

// Exercise the public OpenStream path, not just connectError in isolation.
// A custom dialer returns a deterministic timeout without any cloud calls.
func TestOpenStreamClassifiesWebSocketHandshakeTimeout(t *testing.T) {
	cause := injectedDialTimeout{}
	client, err := New(Config{
		APIKey:   "test-key",
		Endpoint: "ws://127.0.0.1:1",
		Dialer: &websocket.Dialer{
			NetDialContext: func(context.Context, string, string) (net.Conn, error) {
				return nil, cause
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := client.OpenStream(context.Background(), asr.Config{
		Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
	})
	if stream != nil {
		t.Fatalf("failed handshake returned non-nil stream: %T", stream)
	}
	if !speecherr.IsCode(err, speecherr.CodeTimeout) || !errors.Is(err, cause) {
		t.Fatalf("dial timeout was misclassified or lost its cause: %v", err)
	}
	var structured *speecherr.Error
	if !errors.As(err, &structured) {
		t.Fatalf("missing structured error: %v", err)
	}
	if structured.Provider != "volcengine" || structured.Operation != "connect" || !structured.Retryable {
		t.Fatalf("unexpected handshake timeout metadata: %+v", structured)
	}
}

func TestConnectErrorPreservesHandshakeHTTPClassification(t *testing.T) {
	cause := injectedDialTimeout{}
	for _, tc := range []struct {
		name     string
		status   int
		wantCode speecherr.Code
		retry    bool
	}{
		{"unauthorized", http.StatusUnauthorized, speecherr.CodeUnauthorized, false},
		{"rate_limited", http.StatusTooManyRequests, speecherr.CodeRateLimited, true},
		{"invalid_request", http.StatusBadRequest, speecherr.CodeInvalidArgument, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := connectError(cause, &http.Response{
				StatusCode: tc.status,
				Header: http.Header{
					"X-Api-Status-Code": []string{"vendor-status"},
					"X-Tt-Logid": []string{"vendor-logid"},
				},
			})
			var structured *speecherr.Error
			if !errors.As(err, &structured) {
				t.Fatalf("no structured response error: %v", err)
			}
			if structured.Code != tc.wantCode || structured.Retryable != tc.retry ||
				structured.RemoteCode != "vendor-status" {
				t.Fatalf("HTTP %d misclassified: %+v", tc.status, structured)
			}
			if !errors.Is(err, cause) {
				t.Fatalf("HTTP response error lost underlying cause: %v", err)
			}
		})
	}
}
