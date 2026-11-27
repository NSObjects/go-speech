//go:build integration

package volcengine

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"sync/atomic"
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
	var dials atomic.Int32
	dialer := *websocket.DefaultDialer
	dialer.NetDialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dials.Add(1)
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	c, err := New(Config{APIKey: key, ModelID: os.Getenv("VOLCENGINE_TTS_MODEL_ID"), LanguageCode: "zh", Pool: &transport.PoolConfig{MaxOpen: 1}, Dialer: &dialer})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	warm, stop := context.WithTimeout(ctx, 10*time.Second)
	if err := c.Warmup(warm, voice, 1); err != nil {
		stop()
		t.Fatal("warmup:", err)
	}
	stop()
	if err := c.Warmup(ctx, voice, 1); err != nil {
		t.Fatal(err)
	}
	// Validate a fresh (not previously used) connection near the default
	// idle budget, without adding an extra synthesis request.
	t.Logf("waiting 8s after fresh warmup")
	time.Sleep(8 * time.Second)
	if dials.Load() != 1 {
		t.Fatal("warmup did not open exactly one socket")
	}
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
		if dials.Load() != 1 {
			t.Fatal("synthesis did not use prewarmed socket")
		}
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
