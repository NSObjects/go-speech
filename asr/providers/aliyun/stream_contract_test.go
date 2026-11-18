package aliyun

import (
	"context"
	"testing"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/asr/internal/contracttest"
	"github.com/NSObjects/go-speech/audio"
	"github.com/gorilla/websocket"
)

func TestSharedASRStreamLifecycleContract(t *testing.T) {
	contracttest.RunStreamLifecycle(t, func(t *testing.T, parent context.Context) asr.Stream {
		t.Helper()
		endpoint := lifecycleEndpoint(t, func(ws *websocket.Conn, _ string) {
			for {
				if _, _, err := ws.ReadMessage(); err != nil {
					return
				}
			}
		})
		client, err := New(Config{
			Endpoint: endpoint, APIKey: "test", Model: ModelFunASRRealtime,
		})
		if err != nil {
			t.Fatal(err)
		}
		session, err := client.OpenStream(parent, asr.Config{
			Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
		})
		if err != nil {
			t.Fatal(err)
		}
		return session
	})
}
