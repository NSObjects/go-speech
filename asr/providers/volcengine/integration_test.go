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
// Two different synthetic utterances separated by silence are streamed at
// approximately real-time speed. The test verifies both distinct sentences
// survive with no repeated full-mode transcription. It is omitted from ordinary
// go test; automatic paid CI uses repository Actions credentials.
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
			if resultType == ResultSingle {
				// Verify provider-side 700ms silence segmentation against the real service.
				cfg.EndWindowSize = 700 * time.Millisecond
			}
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
				finals   []string
				partials int
				err      error
			}
			done := make(chan result, 1)
			go func() {
				var finals []string
				partials := 0
				for {
					event, err := stream.Recv(ctx)
					if errors.Is(err, io.EOF) {
						done <- result{finals: finals, partials: partials}
						return
					}
					if err != nil {
						done <- result{err: err}
						return
					}
					switch event.Type {
					case asr.EventFinal:
						finals = append(finals, event.Text)
					case asr.EventPartial:
						partials++
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
				joined := strings.ToLower(strings.Join(received.finals, " "))
				t.Logf("Volcengine ASR finals=%q, partial event count=%d", received.finals, received.partials)
				if len(received.finals) < 2 {
					t.Fatalf("expected two distinct confirmed utterances, received %d: %q", len(received.finals), received.finals)
				}
				appleIndex, carIndex := strings.Index(joined, "apple"), strings.Index(joined, "car")
				if appleIndex < 0 || carIndex < 0 || appleIndex >= carIndex {
					t.Fatalf("missing or reordered utterances; recognized %q", joined)
				}
				if strings.Count(joined, "apple") != 1 || strings.Count(joined, "car") != 1 {
					t.Fatalf("detected repeated transcript text in confirmed segments: %q", joined)
				}
			case <-ctx.Done():
				t.Fatalf("waiting for final Volcengine ASR result: %v", ctx.Err())
			}
		})
	}
}

// TestVolcengineLiveSilence verifies that pure PCM silence never becomes
// confirmed recognized speech, including upstream no-speech responses (1013).
func TestVolcengineLiveSilence(t *testing.T) {
	apiKey := os.Getenv("VOLCENGINE_API_KEY")
	if apiKey == "" {
		t.Fatal("VOLCENGINE_API_KEY is required")
	}
	cfg := Config{
		APIKey:     apiKey,
		ResourceID: os.Getenv("VOLCENGINE_RESOURCE_ID"),
		ResultType: ResultSingle,
	}
	if endpoint := os.Getenv("VOLCENGINE_WS_ENDPOINT"); endpoint != "" {
		cfg.Endpoint = endpoint
	}
	client, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stream, err := client.OpenStream(ctx, asr.Config{
		Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
	})
	if err != nil {
		t.Fatalf("connect Volcengine for silence: %v", err)
	}
	defer stream.Close()

	// Five 200ms chunks of signed 16-bit zero PCM = 1s of silence.
	silenceChunk := make([]byte, 16000*2/5)
	for i := 0; i < 5; i++ {
		if err := stream.Send(ctx, silenceChunk); err != nil {
			t.Fatalf("send silence frame %d: %v", i, err)
		}
		select {
		case <-time.After(200 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if err := stream.CloseSend(ctx); err != nil {
		t.Fatalf("close silence stream input: %v", err)
	}
	for {
		event, err := stream.Recv(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("silent ASR stream terminated unexpectedly: %v", err)
		}
		if event.Type == asr.EventFinal && strings.TrimSpace(event.Text) != "" {
			t.Fatalf("pure silence produced confirmed transcript %q", event.Text)
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := stream.Recv(context.Background()); !errors.Is(err, io.EOF) {
			t.Fatalf("unstable EOF after silence, call %d: %v", i, err)
		}
	}
}
