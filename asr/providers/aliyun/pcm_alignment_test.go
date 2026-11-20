package aliyun

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/gorilla/websocket"
)

func TestPCMFrameAlignmentAndEncodedByteStreaming(t *testing.T) {
	tests := []struct {
		name     string
		encoding audio.Encoding
		invalid  []byte
		valid    []byte
	}{
		{
			name: "PCM16LE rejects a partial 16-bit sample",
			encoding: audio.EncodingPCM16LE,
			invalid: []byte{1},
			valid: []byte{1, 2},
		},
		{
			name: "Ogg Opus remains arbitrarily chunkable",
			encoding: audio.EncodingOpus,
			valid: []byte{1, 2, 3},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := make(chan error, 1)
			endpoint := lifecycleEndpoint(t, func(ws *websocket.Conn, _ string) {
				typ, payload, err := ws.ReadMessage()
				if err != nil {
					got <- err
					return
				}
				if typ != websocket.BinaryMessage || !bytes.Equal(payload, tc.valid) {
					got <- fmt.Errorf("unexpected first audio frame: type=%d data=%x, want %x", typ, payload, tc.valid)
					return
				}
				got <- nil
				for {
					if _, _, err := ws.ReadMessage(); err != nil {
						return
					}
				}
			})
			client, err := New(Config{Endpoint: endpoint, APIKey: "test", Model: ModelFunASRRealtime})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			stream, err := client.OpenStream(ctx, asr.Config{
				Format: audio.Format{Encoding: tc.encoding, SampleRate: 16000, Channels: 1},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()

			if tc.invalid != nil {
				if err := stream.Send(ctx, tc.invalid); !speecherr.IsCode(err, speecherr.CodeInvalidArgument) {
					t.Fatalf("partial PCM frame should fail locally, got: %v", err)
				}
			}
			if err := stream.Send(ctx, tc.valid); err != nil {
				t.Fatalf("valid bytes rejected after local validation: %v", err)
			}
			select {
			case err := <-got:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatalf("provider did not receive valid first frame: %v", ctx.Err())
			}
		})
	}
}
