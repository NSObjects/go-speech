//go:build integration

package volcengine

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/transport"
	"github.com/NSObjects/go-speech/tts"
	"github.com/gorilla/websocket"
)

func TestVolcengineLivePool(t *testing.T) {
	key, voice := os.Getenv("VOLCENGINE_API_KEY"), os.Getenv("VOLCENGINE_TTS_VOICE")
	if key == "" || voice == "" {
		t.Fatal("Seed-TTS credentials and voice required")
	}
	c, err := New(Config{APIKey: key, ModelID: os.Getenv("VOLCENGINE_TTS_MODEL_ID"), LanguageCode: "zh", Pool: &transport.PoolConfig{MaxOpen: 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var conn *websocket.Conn
	for i, f := range []audio.Format{
		{Encoding: audio.EncodingPCM16LE, SampleRate: 24000, Channels: 1},
		{Encoding: audio.EncodingOpus, SampleRate: 48000, Channels: 1},
	} {
		start := time.Now()
		s, err := c.OpenDuplex(ctx, tts.DuplexRequest{Voice: voice, Format: f})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		current := s.(*duplex).conn
		if i != 0 && current != conn {
			t.Fatal("different physical WebSocket")
		}
		conn = current
		t.Logf("session %d ready including acquisition: %s", i+1, time.Since(start))
		if err := s.SendText(ctx, "你好，这是连接复用测试。"); err != nil {
			t.Fatal(err)
		}
		if err := s.CloseSend(ctx); err != nil {
			t.Fatal(err)
		}
		var data []byte
		for {
			chunk, err := s.Recv(ctx)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(data) == 0 {
				t.Logf("session %d first audio including acquisition: %s", i+1, time.Since(start))
			}
			data = append(data, chunk.Data...)
		}
		if i == 0 {
			verifyAdvancedPCM(t, data)
		} else {
			verifyLiveOpus(t, data)
		}
		_ = s.Close()
	}
	aborted, err := c.OpenDuplex(ctx, tts.DuplexRequest{Voice: voice})
	if err != nil {
		t.Fatal(err)
	}
	if aborted.(*duplex).conn != conn {
		t.Fatal("expected reused connection before abort")
	}
	_ = aborted.Close()
	fresh, err := c.OpenDuplex(ctx, tts.DuplexRequest{Voice: voice})
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if fresh.(*duplex).conn == conn {
		t.Fatal("aborted socket reused")
	}
	t.Log("same socket completed PCM and Opus sessions; aborted socket discarded")
}
