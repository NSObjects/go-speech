//go:build integration

package aliyun

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
)

// TestAliyunLiveASR intentionally uses paid production infrastructure.
// Run it only via the manually triggered integration workflow or with
// -tags=integration and a real recorded/synthesized PCM16LE speech fixture.
// It never runs as part of the ordinary PR/push CI.
func TestAliyunLiveASR(t *testing.T) {
	apiKey := os.Getenv("DASHSCOPE_API_KEY")
	endpoint := os.Getenv("DASHSCOPE_WS_ENDPOINT")
	model := os.Getenv("DASHSCOPE_ASR_MODEL")
	path := os.Getenv("ASR_TEST_PCM_FILE")
	if apiKey == "" || endpoint == "" || model == "" || path == "" {
		t.Fatal("DASHSCOPE_API_KEY, DASHSCOPE_WS_ENDPOINT, DASHSCOPE_ASR_MODEL and ASR_TEST_PCM_FILE are required")
	}
	sampleRate := 16000
	if model == ModelParaformerRealtime8K {
		sampleRate = 8000
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := audio.ValidateBuffer(audio.Buffer{Data: raw, Format: audio.Format{
		Encoding: audio.EncodingPCM16LE, SampleRate: sampleRate, Channels: 1,
	}}); err != nil {
		t.Fatalf("fixture is not valid PCM16LE: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client, err := New(Config{Endpoint: endpoint, APIKey: apiKey, Model: model})
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.OpenStream(ctx, asr.Config{
		Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: sampleRate, Channels: 1},
	})
	if err != nil {
		t.Fatalf("open session (%s): %v", model, err)
	}
	defer session.Close()

	type receiveResult struct {
		text string
		err  error
	}
	receiveDone := make(chan receiveResult, 1)
	go func() {
		var finalText []string
		for {
			event, err := session.Recv(ctx)
			if errors.Is(err, io.EOF) {
				receiveDone <- receiveResult{text: strings.Join(finalText, " ")}
				return
			}
			if err != nil {
				receiveDone <- receiveResult{err: err}
				return
			}
			if event.Type == asr.EventFinal {
				finalText = append(finalText, event.Text)
			}
		}
	}()

	chunkBytes := sampleRate * 2 / 10 // 100ms of 16-bit mono audio
	for offset := 0; offset < len(raw); offset += chunkBytes {
		end := offset + chunkBytes
		if end > len(raw) {
			end = len(raw)
		}
		if err := session.Send(ctx, raw[offset:end]); err != nil {
			t.Fatalf("send speech audio at byte %d: %v", offset, err)
		}
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			t.Fatalf("sending audio timed out: %v", ctx.Err())
		}
	}
	if err := session.CloseSend(ctx); err != nil {
		t.Fatalf("finish ASR task: %v", err)
	}
	select {
	case received := <-receiveDone:
		if received.err != nil {
			t.Fatalf("receive ASR results: %v", received.err)
		}
		if strings.TrimSpace(received.text) == "" {
			t.Fatalf("ASR returned no final text for %s (the fixture must contain intelligible speech)", model)
		}
		t.Log(fmt.Sprintf("ASR model %s recognized: %q", model, received.text))
	case <-ctx.Done():
		t.Fatalf("waiting for final ASR results: %v", ctx.Err())
	}
}
