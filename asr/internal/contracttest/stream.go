// Package contracttest contains shared provider-agnostic ASR stream lifecycle
// checks. It is internal to go-speech: providers must implement the contract,
// but applications do not need a testing framework in their public API.
package contracttest

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
)

// OpenStream opens a session against a provider-specific local WebSocket mock.
// The mock must complete the protocol handshake, then stay connected without
// producing recognition events until the client closes the connection.
type OpenStream func(t *testing.T, parent context.Context) asr.Stream

// RunStreamLifecycle verifies the same cancellation, close and half-close
// semantics against every streaming ASR provider. Provider-specific protocol,
// transcript and error-classification tests remain in their own packages.
func RunStreamLifecycle(t *testing.T, open OpenStream) {
	t.Helper()
	t.Run("explicit_close_is_idempotent", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s := open(t, ctx)
		defer s.Close()
		for i := 0; i < 2; i++ {
			if err := s.Close(); err != nil {
				t.Fatalf("Close %d: %v", i, err)
			}
		}
		for _, packet := range [][]byte{{1, 0}, nil} {
			if err := s.Send(context.Background(), packet); !errors.Is(err, asr.ErrStreamClosed) {
				t.Fatalf("Send on explicitly closed stream (length %d) = %v", len(packet), err)
			}
		}
		if err := s.CloseSend(context.Background()); !errors.Is(err, asr.ErrStreamClosed) {
			t.Fatalf("CloseSend on closed stream = %v", err)
		}
		if _, err := s.Recv(context.Background()); !errors.Is(err, asr.ErrStreamClosed) {
			t.Fatalf("Recv on closed stream = %v", err)
		}
	})

	t.Run("half_close_then_explicit_close", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s := open(t, ctx)
		defer s.Close()
		if err := s.Send(ctx, []byte{1, 0}); err != nil {
			t.Fatal(err)
		}
		if err := s.CloseSend(ctx); err != nil {
			t.Fatal(err)
		}
		if err := s.CloseSend(ctx); err != nil {
			t.Fatalf("CloseSend not idempotent: %v", err)
		}
		if err := s.Send(ctx, []byte{2, 0}); !errors.Is(err, asr.ErrInputClosed) {
			t.Fatalf("Send after CloseSend = %v", err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		for _, packet := range [][]byte{{3, 0}, nil} {
			if err := s.Send(context.Background(), packet); !errors.Is(err, asr.ErrStreamClosed) {
				t.Fatalf("Send after half-close and Close (length %d) = %v", len(packet), err)
			}
		}
	})

	t.Run("pre_canceled_recv_retains_original_cause", func(t *testing.T) {
		parent, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		s := open(t, parent)
		defer s.Close()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := s.Recv(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("first Recv must return context.Canceled, got %v", err)
		}
		secondCtx, stopSecond := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer stopSecond()
		if _, err := s.Recv(secondCtx); !errors.Is(err, context.Canceled) {
			t.Fatalf("second Recv lost original context cancellation, got %v", err)
		}
		if err := s.Send(parent, []byte{1, 0}); !errors.Is(err, asr.ErrStreamClosed) {
			t.Fatalf("Send after canceled Recv must be closed, got %v", err)
		}
	})

	t.Run("recv_deadline_retains_original_cause", func(t *testing.T) {
		parent, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		s := open(t, parent)
		defer s.Close()
		ctx, cancel := context.WithTimeout(parent, 100*time.Millisecond)
		defer cancel()
		if _, err := s.Recv(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("first Recv must return context.DeadlineExceeded, got %v", err)
		}
		secondCtx, stopSecond := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer stopSecond()
		if _, err := s.Recv(secondCtx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("second Recv lost original deadline error, got %v", err)
		}
	})

	// A canceled upload call must terminate the session even if its context
	// was canceled before the WebSocket write. Keep the first error observable.
	for _, tc := range []struct {
		name string
		newContext func() (context.Context, context.CancelFunc)
	}{
		{
			name: "canceled",
			newContext: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, cancel
			},
		},
		{
			name: "deadline",
			newContext: func() (context.Context, context.CancelFunc) {
				return context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			},
		},
	} {
		for _, operation := range []string{"send", "close_send"} {
			t.Run("pre_"+tc.name+"_"+operation+"_closes_session", func(t *testing.T) {
				parent, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				s := open(t, parent)
				defer s.Close()
				if operation == "close_send" {
					if err := s.Send(parent, []byte{1, 0}); err != nil {
						t.Fatalf("initial Send: %v", err)
					}
				}
				ctx, cancel := tc.newContext()
				defer cancel()
				var first error
				if operation == "send" {
					first = s.Send(ctx, []byte{1, 0})
				} else {
					first = s.CloseSend(ctx)
				}
				if !errors.Is(first, ctx.Err()) {
					t.Fatalf("%s must preserve %v, got %v", operation, ctx.Err(), first)
				}
				for i := 0; i < 2; i++ {
					_, got := s.Recv(context.Background())
					if got != first {
						t.Fatalf("Recv %d changed terminal error: %v vs %v", i, got, first)
					}
				}
				if err := s.Send(context.Background(), []byte{2, 0}); !errors.Is(err, asr.ErrStreamClosed) {
					t.Fatalf("Send after canceled %s: %v", operation, err)
				}
				if err := s.CloseSend(context.Background()); !errors.Is(err, asr.ErrStreamClosed) {
					t.Fatalf("CloseSend after canceled %s: %v", operation, err)
				}
			})
		}
	}

	t.Run("parent_cancel_terminates_recv", func(t *testing.T) {
		parent, cancel := context.WithCancel(context.Background())
		s := open(t, parent)
		defer s.Close()
		cancel()
		for i := 0; i < 2; i++ {
			callCtx, stop := context.WithTimeout(context.Background(), 500*time.Millisecond)
			_, err := s.Recv(callCtx)
			stop()
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Recv %d after parent cancellation: %v", i, err)
			}
		}
		if err := s.Send(context.Background(), []byte{1, 0}); !errors.Is(err, context.Canceled) {
			t.Fatalf("Send after parent cancellation = %v", err)
		}
	})
}

// RequireStableEOF is useful for provider-specific mocks that send a complete
// task result. A finished Stream must never turn EOF into a close/network error.
func RequireStableEOF(t *testing.T, stream asr.Stream) {
	t.Helper()
	for i := 0; i < 3; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		_, err := stream.Recv(ctx)
		cancel()
		if !errors.Is(err, io.EOF) {
			t.Fatalf("Recv %d after terminal EOF = %v", i, err)
		}
	}
}
