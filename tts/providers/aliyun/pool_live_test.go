//go:build integration

package aliyun

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/transport"
	"github.com/NSObjects/go-speech/tts"
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
	req.Text = "你好，欢迎使用语音合成。"
	for round := 1; round <= 2; round++ {
		start := time.Now()
		stream, err := client.SynthesizeStream(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
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
				t.Logf("round %d first audio including acquisition and task start: %s", round, time.Since(start))
			}
			total += len(chunk.Data)
		}
		_ = stream.Close()
		if total < 1024 {
			t.Fatalf("insufficient audio: %d", total)
		}
	}
	// Interrupt a genuinely speaking duplex task, not just an empty session.
	// Drain its terminal acknowledgement before acquiring the socket again.
	duplex, err := client.OpenDuplex(ctx, tts.DuplexRequest{Voice: req.Voice, Format: req.Format})
	if err != nil {
		t.Fatal(err)
	}
	defer duplex.Close()
	if duplex.(*Stream).conn != conn {
		t.Fatal("duplex did not acquire the existing connection")
	}
	if err := duplex.SendText(ctx, strings.Repeat("这是一段用于测试语音打断和连接复用的文本。", 8)); err != nil {
		t.Fatal(err)
	}
	chunk, err := duplex.Recv(ctx)
	if err != nil || len(chunk.Data) == 0 {
		t.Fatalf("audio before cancellation: bytes=%d err=%v", len(chunk.Data), err)
	}
	cancelStart := time.Now()
	if err := duplex.(tts.Canceler).Cancel(ctx); err != nil {
		t.Fatal(err)
	}
	for {
		_, err := duplex.Recv(ctx)
		if errors.Is(err, tts.ErrSynthesisCanceled) {
			break
		}
		if err != nil {
			t.Fatalf("await cancellation acknowledgement: %v", err)
		}
	}
	t.Logf("native cancellation acknowledgement: %s", time.Since(cancelStart))
	if err := duplex.Close(); err != nil {
		t.Fatal(err)
	}
	stream, err := client.SynthesizeStream(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if stream.(*Stream).conn != conn {
		t.Fatal("connection not reused after acknowledged cancellation")
	}
	total := 0
	for {
		chunk, err := stream.Recv(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		total += len(chunk.Data)
	}
	if total < 1024 {
		t.Fatalf("insufficient audio after cancellation: %d", total)
	}
	t.Log("same physical WebSocket completed synthesis after cancellation")
}
