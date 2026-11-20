package aliyun

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/gorilla/websocket"
)

type aliyunInjectedDialTimeout struct{}

func (aliyunInjectedDialTimeout) Error() string   { return "injected Aliyun WebSocket dial timeout" }
func (aliyunInjectedDialTimeout) Timeout() bool   { return true }
func (aliyunInjectedDialTimeout) Temporary() bool { return true }

// Test the public entry point. A dial timeout is not necessarily
// context.DeadlineExceeded: it can come from the socket handshake itself.
func TestOpenStreamPreservesAliyunHandshakeTimeout(t *testing.T) {
	cause := aliyunInjectedDialTimeout{}
	client, err := New(Config{
		Endpoint: "ws://127.0.0.1:1/api-ws/v1/inference",
		APIKey:   "test-key",
		Dialer: &websocket.Dialer{
			NetDialContext: func(context.Context, string, string) (net.Conn, error) {
				return nil, fmt.Errorf("dial: %w", cause)
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
		t.Fatalf("dial failure returned a stream: %T", stream)
	}
	if !speecherr.IsCode(err, speecherr.CodeTimeout) || !errors.Is(err, cause) {
		t.Fatalf("handshake timeout misclassified or cause lost: %v", err)
	}
	var classified *speecherr.Error
	if !errors.As(err, &classified) {
		t.Fatalf("expected provider error: %v", err)
	}
	if classified.Provider != "aliyun" || classified.Operation != "connect" || !classified.Retryable {
		t.Fatalf("unexpected handshake timeout metadata: %+v", classified)
	}
}

func TestAliyunDialErrorHTTPStatusTakesPrecedence(t *testing.T) {
	cause := aliyunInjectedDialTimeout{}
	for _, tc := range []struct {
		name      string
		status    int
		code      speecherr.Code
		retryable bool
	}{
		{"unauthorized", http.StatusUnauthorized, speecherr.CodeUnauthorized, false},
		{"forbidden", http.StatusForbidden, speecherr.CodeUnauthorized, false},
		{"rate_limited", http.StatusTooManyRequests, speecherr.CodeRateLimited, true},
		{"bad_request", http.StatusBadRequest, speecherr.CodeInvalidArgument, false},
		{"unavailable", http.StatusServiceUnavailable, speecherr.CodeUnavailable, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Even if the cause looks like a timeout, an actual HTTP response
			// is authoritative for provider error classification.
			err := dialError(cause, &http.Response{StatusCode: tc.status})
			var classified *speecherr.Error
			if !errors.As(err, &classified) {
				t.Fatalf("missing structured error: %v", err)
			}
			if classified.Code != tc.code || classified.Retryable != tc.retryable {
				t.Fatalf("HTTP %d: got %+v, want %s retryable=%v",
					tc.status, classified, tc.code, tc.retryable)
			}
			if !errors.Is(err, cause) {
				t.Fatalf("HTTP %d discarded original cause: %v", tc.status, err)
			}
		})
	}
}

func TestAliyunDialErrorContextDeadlineWithoutHTTP(t *testing.T) {
	err := dialError(fmt.Errorf("handshake: %w", context.DeadlineExceeded), nil)
	if !speecherr.IsCode(err, speecherr.CodeTimeout) ||
		!errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("context deadline was not preserved: %v", err)
	}
}
