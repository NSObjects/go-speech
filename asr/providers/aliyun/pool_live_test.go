//go:build integration

package aliyun

import (
	"context"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/transport"
	"github.com/gorilla/websocket"
)

func TestAliyunLivePool(t *testing.T) {
	key, endpoint := os.Getenv("DASHSCOPE_API_KEY"), os.Getenv("DASHSCOPE_WS_ENDPOINT")
	model, path := os.Getenv("DASHSCOPE_ASR_MODEL"), os.Getenv("ASR_TEST_PCM_FILE")
	if key == "" || endpoint == "" || model == "" || path == "" {
		t.Fatal("Aliyun credentials, model and fixture required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rate := 16000
	if model == ModelParaformerRealtime8K {
		rate = 8000
	}
	var dials atomic.Int32
	d := *websocket.DefaultDialer
	d.NetDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		dials.Add(1)
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	client, err := New(Config{
		APIKey: key, Endpoint: endpoint, Model: model, Dialer: &d,
		Pool: &transport.PoolConfig{MaxOpen: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	input := audio.Buffer{Data: raw, Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: rate, Channels: 1}}
	for round := 1; round <= 2; round++ {
		start := time.Now()
		result, err := client.Recognize(ctx, input, asr.Config{})
		if err != nil {
			t.Fatalf("round %d model=%s physical_dials=%d elapsed=%s: %v", round, model, dials.Load(), time.Since(start), err)
		}
		if strings.TrimSpace(result.Text) == "" {
			t.Fatalf("round %d: no final transcript", round)
		}
		t.Logf("round %d model=%s physical_dials=%d elapsed=%s", round, model, dials.Load(), time.Since(start))
	}
	if got := dials.Load(); got != 1 {
		t.Fatalf("expected one physical connection, got %d", got)
	}
}
