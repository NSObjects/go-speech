//go:build integration

package aliyun

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/transport"
	"github.com/gorilla/websocket"
)

func TestAliyunLivePool(t *testing.T) {
	key, endpoint := os.Getenv("DASHSCOPE_API_KEY"), os.Getenv("DASHSCOPE_WS_ENDPOINT")
	if key == "" || endpoint == "" {
		t.Fatal("Aliyun live credentials required")
	}
	client, err := New(Config{
		APIKey: key, Endpoint: endpoint, Model: os.Getenv("DASHSCOPE_TTS_MODEL"),
		Pool: &transport.PoolConfig{MaxOpen: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	req := testRequest()
	if voice := os.Getenv("DASHSCOPE_TTS_VOICE"); voice != "" {
		req.Voice = voice
	}
	var conn *websocket.Conn
	for _, text := range []string{"你好，欢迎使用语音合成。", "这是第二个独立的语音任务。"} {
		req.Text = text
		start := time.Now()
		stream, err := client.SynthesizeStream(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		session := stream.(*Stream)
		if conn != nil && conn != session.conn {
			t.Fatal("physical connection not reused")
		}
		conn = session.conn
		total := 0
		for {
			chunk, err := stream.Recv(ctx)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if total == 0 {
				t.Logf("first audio including acquisition and task start: %s", time.Since(start))
			}
			total += len(chunk.Data)
		}
		_ = stream.Close()
		if total < 1024 {
			t.Fatalf("insufficient audio: %d", total)
		}
	}
}
