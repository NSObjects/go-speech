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

// TestVolcengineLiveMandarin exercises real Seed-ASR 2.0 with offline
// synthesized Mandarin PCM. It validates that Chinese speech is recognized
// using the service's automatic language handling (no Language override).
// Synthetic speech is not a substitute for child-voice or noisy-room tests.
func TestVolcengineLiveMandarin(t *testing.T) {
	apiKey := os.Getenv("VOLCENGINE_API_KEY")
	if apiKey == "" {
		t.Fatal("VOLCENGINE_API_KEY is required")
	}

	cases := []struct {
		name       string
		file       string
		resultType string
		want       string
	}{
		{name: "short_greeting", file: "VOLCENGINE_MANDARIN_SHORT_PCM_FILE", resultType: ResultSingle, want: "你好"},
		{name: "sentence_full", file: "VOLCENGINE_MANDARIN_PCM_FILE", resultType: ResultFull, want: "天气"},
		{name: "sentence_single", file: "VOLCENGINE_MANDARIN_PCM_FILE", resultType: ResultSingle, want: "天气"},
	}
	format := audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := os.Getenv(tc.file)
			if path == "" {
				t.Fatalf("%s is required", tc.file)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := audio.ValidateBuffer(audio.Buffer{Data: raw, Format: format}); err != nil {
				t.Fatal(err)
			}
			cfg := Config{
				APIKey:          apiKey,
				ResourceID:      os.Getenv("VOLCENGINE_RESOURCE_ID"),
				ResultType:      tc.resultType,
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
				t.Fatalf("open Mandarin ASR: %v", err)
			}
			defer stream.Close()

			type result struct {
				finals []string
				err    error
			}
			done := make(chan result, 1)
			go func() {
				var finals []string
				for {
					event, err := stream.Recv(ctx)
					if errors.Is(err, io.EOF) {
						done <- result{finals: finals}
						return
					}
					if err != nil {
						done <- result{err: err}
						return
					}
					if event.Type == asr.EventFinal {
						finals = append(finals, event.Text)
					}
				}
			}()

			const chunk = 16000 * 2 / 5 // 200ms PCM16LE mono
			for offset := 0; offset < len(raw); offset += chunk {
				end := offset + chunk
				if end > len(raw) {
					end = len(raw)
				}
				if err := stream.Send(ctx, raw[offset:end]); err != nil {
					t.Fatalf("send Mandarin audio at %d: %v", offset, err)
				}
				select {
				case <-time.After(200 * time.Millisecond):
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			if err := stream.CloseSend(ctx); err != nil {
				t.Fatalf("finish Mandarin audio: %v", err)
			}
			select {
			case got := <-done:
				if got.err != nil {
					t.Fatal(got.err)
				}
				recognized := strings.Join(got.finals, "")
				t.Logf("Mandarin %s finals=%q", tc.name, got.finals)
				if !strings.Contains(recognized, tc.want) {
					t.Fatalf("expected Chinese phrase containing %q, got %q", tc.want, recognized)
				}
			case <-ctx.Done():
				t.Fatalf("wait for Mandarin ASR final: %v", ctx.Err())
			}
		})
	}
}
