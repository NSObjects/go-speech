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

// TestVolcengineLiveMinuteSession streams at least 60 seconds of real-time
// synthetic Mandarin on one WebSocket. This is a bounded connection endurance
// check, NOT a production load test or a recorded child-voice accuracy test.
func TestVolcengineLiveMinuteSession(t *testing.T) {
	apiKey := os.Getenv("VOLCENGINE_API_KEY")
	path := os.Getenv("VOLCENGINE_MINUTE_PCM_FILE")
	if apiKey == "" || path == "" {
		t.Fatal("VOLCENGINE_API_KEY and VOLCENGINE_MINUTE_PCM_FILE are required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	format := audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1}
	if err := audio.ValidateBuffer(audio.Buffer{Data: raw, Format: format}); err != nil {
		t.Fatal(err)
	}
	const bytesPerSecond = 16000 * 2
	if len(raw) < 60*bytesPerSecond {
		t.Fatalf("expected at least 60s of PCM, got %.1fs", float64(len(raw))/bytesPerSecond)
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
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	stream, err := client.OpenStream(ctx, asr.Config{Format: format})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	type result struct {
		finals     []string
		partials   int
		firstFinal time.Duration
		err        error
	}
	done := make(chan result, 1)
	started := time.Now()
	go func() {
		got := result{}
		for {
			event, err := stream.Recv(ctx)
			if errors.Is(err, io.EOF) {
				done <- got
				return
			}
			if err != nil {
				got.err = err
				done <- got
				return
			}
			switch event.Type {
			case asr.EventPartial:
				got.partials++
			case asr.EventFinal:
				if len(got.finals) == 0 {
					got.firstFinal = time.Since(started)
				}
				got.finals = append(got.finals, event.Text)
			}
		}
	}()

	const frameBytes = bytesPerSecond / 5 // 200ms PCM16LE mono
	for offset := 0; offset < len(raw); offset += frameBytes {
		end := offset + frameBytes
		if end > len(raw) {
			end = len(raw)
		}
		if err := stream.Send(ctx, raw[offset:end]); err != nil {
			t.Fatalf("Send at offset %d (%.2fs): %v", offset, float64(offset)/bytesPerSecond, err)
		}
		select {
		case <-time.After(200 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if err := stream.CloseSend(ctx); err != nil {
		t.Fatalf("CloseSend after %.1fs: %v", float64(len(raw))/bytesPerSecond, err)
	}
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("minute-session receive: %v", got.err)
		}
		t.Logf("minute-session audio=%.1fs wall=%.1fs first_final=%.1fs partials=%d finals=%q",
			float64(len(raw))/bytesPerSecond, time.Since(started).Seconds(),
			got.firstFinal.Seconds(), got.partials, got.finals)
		if len(got.finals) < 12 {
			t.Fatalf("expected at least 12 independently finalized phrases, got %d: %q", len(got.finals), got.finals)
		}
		joined := strings.Join(got.finals, "")
		offset := 0
		for i := 0; i < 12; i++ {
			word := "天气"
			if i%2 == 1 {
				word = "游戏"
			}
			at := strings.Index(joined[offset:], word)
			if at < 0 {
				t.Fatalf("missing or reordered phrase %d (%q): %q", i+1, word, joined)
			}
			offset += at + len(word)
		}
		for _, word := range []string{"天气", "游戏"} {
			if n := strings.Count(joined, word); n != 6 {
				t.Fatalf("phrase %q repeated %d times, expected 6: %q", word, n, joined)
			}
		}
		for i := 0; i < 2; i++ {
			if _, err := stream.Recv(context.Background()); !errors.Is(err, io.EOF) {
				t.Fatalf("stable EOF after minute-session %d: %v", i, err)
			}
		}
	case <-ctx.Done():
		t.Fatalf("waiting for minute-session final: %v", ctx.Err())
	}
}
