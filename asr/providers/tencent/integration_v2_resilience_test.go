//go:build integration

package tencent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Tencent V2 recommends one PCM16LE frame every 40ms. liveSession
// honors that interval, consumes Partial/Final concurrently with sending,
// and requires stable io.EOF after Tencent's final=1 acknowledgement.
func tencentV2LiveModel(t *testing.T) string {
	t.Helper()
	model := os.Getenv("TENCENT_V2_ASR_MODEL")
	if model == "" {
		t.Skip("TENCENT_V2_ASR_MODEL is not set; real-cloud 2.0 tests require an entitled engine")
	}
	if !strings.HasSuffix(model, "_2.0") {
		t.Fatalf("V2 live engine must end with _2.0, got %q", model)
	}
	return model
}

// TestTencentLiveV2ConcurrentSessions exercises three independent
// real V2 WebSockets simultaneously. Each must return finalized text and
// stable terminal EOF, without sharing result state across connections.
func TestTencentLiveV2ConcurrentSessions(t *testing.T) {
	model := tencentV2LiveModel(t)
	pcm := livePCMFixture(t, "TENCENT_TEST_16K_PCM_FILE")
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Second)
	defer cancel()

	const sessions = 3
	results := make(chan error, sessions)
	var wg sync.WaitGroup
	for i := 0; i < sessions; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			out, err := liveSession(ctx, model, 16000, 0, pcm)
			if err != nil {
				results <- fmt.Errorf("V2 concurrent stream %d: %w", index, err)
				return
			}
			if len(out.finals) == 0 {
				results <- fmt.Errorf("V2 concurrent stream %d returned no Final", index)
				return
			}
			results <- nil
		}(i)
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Error(err)
		}
	}
	t.Logf("Tencent V2 %s concurrent_streams=%d audio_duration_ms=%d",
		model, sessions, (len(pcm)*1000)/(16000*2))
}

// TestTencentLiveV2MinuteStream checks that the 2.0 protocol can sustain
// a single real WebSocket across the 60-second sentence-splitting boundary.
// It does not assume that a 60s sentence cut terminates the WebSocket.
func TestTencentLiveV2MinuteStream(t *testing.T) {
	model := tencentV2LiveModel(t)
	pcm := livePCMFixture(t, "TENCENT_TEST_16K_PCM_FILE")
	const repetitions = 9
	audio := make([]byte, 0, repetitions*len(pcm))
	for i := 0; i < repetitions; i++ {
		audio = append(audio, pcm...)
	}
	duration := time.Duration(len(audio)) * time.Second / (16000 * 2)
	if duration <= time.Minute {
		t.Fatalf("V2 test must cross one minute of continuous PCM; duration=%v", duration)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 160*time.Second)
	defer cancel()
	result, err := liveSession(ctx, model, 16000, 0, audio)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Tencent V2 %s minute_plus_audio_ms=%d partials=%d finals=%d stable_eof=true",
		model, duration.Milliseconds(), result.partials, len(result.finals))
}
