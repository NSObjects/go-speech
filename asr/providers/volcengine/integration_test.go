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

// TestVolcengineLiveASR exercises the real provider protocol: handshake,
// real-time PCM, both result modes, a nonempty Final and stable EOF.
// It does not grade transcription content or segmentation accuracy.
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
				// Check that an optional endpoint parameter is accepted.
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
				t.Logf("Volcengine ASR finals=%q, partial event count=%d", received.finals, received.partials)
				if strings.TrimSpace(strings.Join(received.finals, " ")) == "" {
					t.Fatal("PCM stream did not produce a nonempty Final event")
				}
				for i := 0; i < 2; i++ {
					if _, err := stream.Recv(context.Background()); !errors.Is(err, io.EOF) {
						t.Fatalf("PCM Recv did not retain EOF on read %d: %v", i, err)
					}
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
