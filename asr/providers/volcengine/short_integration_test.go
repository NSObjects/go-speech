//go:build integration

package volcengine

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
)

// TestVolcengineLiveShortSpeech checks one very short synthesized phrase on
// the real billable service. This is a provider regression, not a measure of
// child speech, acoustic accuracy, or noisy-room performance.
func TestVolcengineLiveShortSpeech(t *testing.T) {
	apiKey := os.Getenv("VOLCENGINE_API_KEY")
	path := os.Getenv("VOLCENGINE_SHORT_PCM_FILE")
	if apiKey == "" || path == "" {
		t.Fatal("VOLCENGINE_API_KEY and VOLCENGINE_SHORT_PCM_FILE are required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	format := audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1}
	if err := audio.ValidateBuffer(audio.Buffer{Data: raw, Format: format}); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		APIKey:          apiKey,
		ResourceID:      os.Getenv("VOLCENGINE_RESOURCE_ID"),
		ResultType:      ResultSingle,
		EnableNonStream: true,
		EndWindowSize:   700 * time.Millisecond,
	}
	if endpoint := os.Getenv("VOLCENGINE_WS_ENDPOINT"); endpoint != "" {
		cfg.Endpoint = endpoint
	}
	client, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	stream, err := client.OpenStream(ctx, asr.Config{Format: format})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	type received struct {
		final []string
		err   error
	}
	done := make(chan received, 1)
	go func() {
		var final []string
		for {
			ev, err := stream.Recv(ctx)
			if errors.Is(err, io.EOF) {
				done <- received{final: final}
				return
			}
			if err != nil {
				done <- received{err: err}
				return
			}
			if ev.Type == asr.EventFinal {
				final = append(final, ev.Text)
			}
		}
	}()

	const chunk = 16000 * 2 / 5 // 200ms, mono PCM16LE
	for offset := 0; offset < len(raw); offset += chunk {
		end := offset + chunk
		if end > len(raw) {
			end = len(raw)
		}
		if err := stream.Send(ctx, raw[offset:end]); err != nil {
			t.Fatalf("send short phrase at offset %d: %v", offset, err)
		}
		select {
		case <-time.After(200 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if err := stream.CloseSend(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("short phrase recognition: %v", res.err)
		}
		t.Logf("short phrase finals: %q", res.final)
		recognized := strings.ToLower(strings.TrimSpace(strings.Join(res.final, " ")))
		if !strings.Contains(recognized, "hello") {
			t.Fatalf("short spoken 'Hello' was not confirmed: %q", recognized)
		}
	case <-ctx.Done():
		t.Fatalf("short phrase final result timeout: %v", ctx.Err())
	}
}
