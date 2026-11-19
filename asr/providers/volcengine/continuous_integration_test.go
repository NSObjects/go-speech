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

// TestVolcengineLiveContinuousMandarin streams four separate natural
// Mandarin utterances over one connection. The silence gaps test provider
// sentence finalization without closing and reopening the WebSocket.
func TestVolcengineLiveContinuousMandarin(t *testing.T) {
	apiKey := os.Getenv("VOLCENGINE_API_KEY")
	path := os.Getenv("VOLCENGINE_CONTINUOUS_PCM_FILE")
	if apiKey == "" || path == "" {
		t.Fatal("VOLCENGINE_API_KEY and VOLCENGINE_CONTINUOUS_PCM_FILE are required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	format := audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1}
	if err := audio.ValidateBuffer(audio.Buffer{Data: raw, Format: format}); err != nil {
		t.Fatal(err)
	}

	for _, mode := range []string{ResultFull, ResultSingle} {
		t.Run(mode, func(t *testing.T) {
			cfg := Config{
				APIKey:          apiKey,
				ResourceID:      os.Getenv("VOLCENGINE_RESOURCE_ID"),
				ResultType:      mode,
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
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			stream, err := client.OpenStream(ctx, asr.Config{Format: format})
			if err != nil {
				t.Fatalf("open long Mandarin session: %v", err)
			}
			defer stream.Close()

			type received struct {
				finals []string
				err    error
			}
			done := make(chan received, 1)
			go func() {
				var finals []string
				for {
					ev, err := stream.Recv(ctx)
					if errors.Is(err, io.EOF) {
						done <- received{finals: finals}
						return
					}
					if err != nil {
						done <- received{err: err}
						return
					}
					if ev.Type == asr.EventFinal {
						finals = append(finals, ev.Text)
					}
				}
			}()

			const frameBytes = 16000 * 2 / 5 // 200ms PCM16LE mono
			for offset := 0; offset < len(raw); offset += frameBytes {
				end := offset + frameBytes
				if end > len(raw) {
					end = len(raw)
				}
				if err := stream.Send(ctx, raw[offset:end]); err != nil {
					t.Fatalf("send long Mandarin stream offset %d: %v", offset, err)
				}
				select {
				case <-time.After(200 * time.Millisecond):
				case <-ctx.Done():
					t.Fatalf("stream long Mandarin audio: %v", ctx.Err())
				}
			}
			if err := stream.CloseSend(ctx); err != nil {
				t.Fatalf("finish long Mandarin stream: %v", err)
			}
			select {
			case got := <-done:
				if got.err != nil {
					t.Fatal(got.err)
				}
				t.Logf("continuous Mandarin %s finals=%q", mode, got.finals)
				if len(got.finals) < 4 {
					t.Fatalf("expected four independently finalized phrases, got %d: %q", len(got.finals), got.finals)
				}
				for i, sentence := range got.finals {
					if !strings.Contains(sentence, "腾讯云") {
						t.Fatalf("final %d missing natural Mandarin phrase: %q", i+1, sentence)
					}
				}
				text := strings.Join(got.finals, "")
				offset := 0
				for _, word := range []string{"腾讯云", "腾讯云", "腾讯云", "腾讯云"} {
					index := strings.Index(text[offset:], word)
					if index < 0 {
						t.Fatalf("missing/reordered keyword %q in final transcript %q", word, text)
					}
					offset += index + len(word)
				}
				// Both phrases are repeated with separate timestamps.
				// The adapter must not collapse a new spoken turn into
				// an old full-mode snapshot.
				for word, want := range map[string]int{"腾讯云": 4} {
					if got := strings.Count(text, word); got != want {
						t.Fatalf("keyword %q appears %d times, want %d: %q", word, got, want, text)
					}
				}
				for i := 0; i < 2; i++ {
					if _, err := stream.Recv(context.Background()); !errors.Is(err, io.EOF) {
						t.Fatalf("unstable EOF after long stream on read %d: %v", i, err)
					}
				}
			case <-ctx.Done():
				t.Fatalf("long Mandarin session did not finish: %v", ctx.Err())
			}
		})
	}
}
