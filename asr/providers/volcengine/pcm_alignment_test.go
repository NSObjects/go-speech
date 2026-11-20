package volcengine

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
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
		format   audio.Format
		invalid  []byte
		valid    []byte
	}{
		{
			name: "mono PCM rejects incomplete sample",
			format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
			invalid: []byte{1},
			valid: []byte{1, 2},
		},
		{
			name: "stereo PCM rejects incomplete channel frame",
			format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 2},
			invalid: []byte{1, 2},
			valid: []byte{1, 2, 3, 4},
		},
		{
			name: "Ogg Opus still accepts arbitrary byte boundaries",
			format: audio.Format{Encoding: audio.EncodingOpus, SampleRate: 16000, Channels: 1},
			valid: []byte{1, 2, 3},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := make(chan error, 1)
			endpoint := mockEndpoint(t, func(ws *websocket.Conn, _ *http.Request) {
				if _, _, err := ws.ReadMessage(); err != nil { // first full request
					got <- err
					return
				}
				typ, encoded, err := ws.ReadMessage()
				if err != nil {
					got <- err
					return
				}
				kind, flags, audioBytes, err := testRequestFrame(encoded)
				if err != nil || typ != websocket.BinaryMessage || kind != messageAudioRequest ||
					flags != 0 || !bytes.Equal(audioBytes, tc.valid) {
					got <- fmt.Errorf("unexpected first audio frame: type=%d kind=%d flags=%d payload=%x, want=%x: %v",
						typ, kind, flags, audioBytes, tc.valid, err)
					return
				}
				got <- nil
				for {
					if _, _, err := ws.ReadMessage(); err != nil {
						return
					}
				}
			})
			client, err := New(Config{Endpoint: endpoint, APIKey: "test"})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			stream, err := client.OpenStream(ctx, asr.Config{Format: tc.format})
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			if tc.invalid != nil {
				if err := stream.Send(ctx, tc.invalid); !speecherr.IsCode(err, speecherr.CodeInvalidArgument) {
					t.Fatalf("partial PCM frame should fail before upload, got: %v", err)
				}
			}
			if err := stream.Send(ctx, tc.valid); err != nil {
				t.Fatalf("valid bytes rejected: %v", err)
			}
			select {
			case err := <-got:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatalf("provider did not receive valid first audio frame: %v", ctx.Err())
			}
		})
	}
}
