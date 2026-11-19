package aliyun

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/gorilla/websocket"
)

var errAliyunInjectedWrite = errors.New("injected aliyun websocket write failure")

type aliyunFaultConn struct {
	net.Conn
	blocked *atomic.Bool
}

func (c *aliyunFaultConn) Write(data []byte) (int, error) {
	if c.blocked.Load() {
		return 0, errAliyunInjectedWrite
	}
	return c.Conn.Write(data)
}

func openAliyunFaultStream(t *testing.T) (asr.Stream, *atomic.Bool) {
	t.Helper()
	endpoint := lifecycleEndpoint(t, func(ws *websocket.Conn, _ string) {
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	})
	var blocked atomic.Bool
	dialer := &net.Dialer{}
	client, err := New(Config{
		Endpoint: endpoint,
		APIKey:   "test-key",
		Model:    ModelFunASRRealtime,
		Dialer: &websocket.Dialer{
			NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				conn, err := dialer.DialContext(ctx, network, addr)
				if err != nil {
					return nil, err
				}
				return &aliyunFaultConn{Conn: conn, blocked: &blocked}, nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	session, err := client.OpenStream(ctx, asr.Config{
		Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, &blocked
}

func TestAliyunWriteFailureRetainedByRecv(t *testing.T) {
	for _, operation := range []string{"send", "close_send", "send_while_recv_blocked"} {
		t.Run(operation, func(t *testing.T) {
			session, blocked := openAliyunFaultStream(t)
			ctx := context.Background()
			if operation == "close_send" {
				if err := session.Send(ctx, []byte{0, 0}); err != nil {
					t.Fatalf("initial Send: %v", err)
				}
			}

			var receiving chan error
			if operation == "send_while_recv_blocked" {
				receiving = make(chan error, 1)
				go func() {
					_, err := session.Recv(context.Background())
					receiving <- err
				}()
			}

			blocked.Store(true)
			var first error
			if operation == "close_send" {
				first = session.CloseSend(ctx)
			} else {
				first = session.Send(ctx, []byte{0, 0})
			}
			if !speecherr.IsCode(first, speecherr.CodeUnavailable) {
				t.Fatalf("%s: expected unavailable transport error, got %v", operation, first)
			}
			if !errors.Is(first, errAliyunInjectedWrite) {
				t.Fatalf("%s: original write cause was lost: %v", operation, first)
			}
			if receiving != nil {
				select {
				case result := <-receiving:
					if result != first {
						t.Fatalf("concurrent Recv lost write error: %v vs %v", result, first)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("concurrent Recv was not unblocked by failed Send")
				}
			}
			for i := 0; i < 3; i++ {
				_, result := session.Recv(context.Background())
				if result != first {
					t.Fatalf("Recv %d: error changed from %v to %v", i, first, result)
				}
			}
			if err := session.Send(ctx, []byte{0, 0}); !errors.Is(err, asr.ErrStreamClosed) {
				t.Fatalf("Send on terminal session: %v", err)
			}
			if err := session.CloseSend(ctx); !errors.Is(err, asr.ErrStreamClosed) {
				t.Fatalf("CloseSend on terminal session: %v", err)
			}
		})
	}
}
