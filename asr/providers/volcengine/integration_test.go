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

// TestVolcengineLiveASR uses a real billable Volcengine service endpoint.
// Synthetic speech is streamed at approximately realtime speed. The test is
// skipped by ordinary go test; automatic online CI runs it when credentials
// are configured in the repository's Actions secrets.
func TestVolcengineLiveASR(t *testing.T) {
	apiKey := os.Getenv("VOLCENGINE_API_KEY")
	fixture := os.Getenv("VOLCENGINE_TEST_PCM_FILE")
	if apiKey == "" || fixture == "" {
		t.Fatal("VOLCENGINE_API_KEY and VOLCENGINE_TEST_PCM_FILE are required")
	}
	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	format := audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1}
	if err := audio.ValidateBuffer(audio.Buffer{Data: raw, Format: format}); err != nil {
		t.Fatal(err)
	}
	// Exercise both official result organizations against the real service.
	// Full repeats committed text; single emits only the current utterance.
	for _, resultType := range []string{ResultFull, ResultSingle} {
		t.Run(resultType, func(t *testing.T) {
		cfg := Config{APIKey: apiKey, ResourceID: os.Getenv("VOLCENGINE_RESOURCE_ID"), EnableNonStream: true, ResultType: resultType}
		if endpoint := os.Getenv("VOLCENGINE_WS_ENDPOINT"); endpoint != "" {
			cfg.Endpoint = endpoint
		}
		c, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		stream, err := c.OpenStream(ctx, asr.Config{Format: format})
		if err != nil {
			t.Fatalf("connect Volcengine: %v", err)
		}
		defer stream.Close()
		type result struct {
			text string
			err  error
		}
		done := make(chan result, 1)
		go func() {
			var final []string
			for {
				event, err := stream.Recv(ctx)
				if errors.Is(err, io.EOF) {
					done <- result{text: strings.Join(final, " ")}
					return
				}
				if err != nil {
					done <- result{err: err}
					return
				}
				if event.Type == asr.EventFinal {
					final = append(final, event.Text)
				}
			}
		}()
		const chunk = 16000 * 2 / 5 // 200ms PCM16 mono
		for offset := 0; offset < len(raw); offset += chunk {
			end := offset + chunk
			if end > len(raw) {
				end = len(raw)
			}
			if err := stream.Send(ctx, raw[offset:end]); err != nil {
				t.Fatalf("send audio at %d: %v", offset, err)
			}
			select {
			case <-time.After(200 * time.Millisecond):
			case <-ctx.Done():
				t.Fatalf("streaming audio timeout: %v", ctx.Err())
			}
		}
		if err := stream.CloseSend(ctx); err != nil {
			t.Fatalf("close audio input: %v", err)
		}
		select {
		case received := <-done:
			if received.err != nil {
				t.Fatalf("Volcengine ASR: %v", received.err)
			}
			if strings.TrimSpace(received.text) == "" {
				t.Fatal("Volcengine returned no confirmed final ASR text")
			}
			t.Logf("Volcengine ASR recognized: %q", received.text)
		case <-ctx.Done():
			t.Fatalf("waiting for final Volcengine ASR result: %v", ctx.Err())
		}
		})
	}
}
