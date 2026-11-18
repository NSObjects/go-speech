//go:build integration

package tencent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
)

type liveResult struct {
	partials int
	finals   []string
}

func livePCMFixture(t *testing.T, envName string) []byte {
	t.Helper()
	path := os.Getenv(envName)
	if path == "" {
		t.Fatalf("%s is required for Tencent live integration", envName)
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) < 3200 {
		t.Fatalf("invalid Tencent PCM fixture %s: bytes=%d err=%v", envName, len(data), err)
	}
	return data
}

func liveSession(ctx context.Context, engine string, rate, inputRate int, pcm []byte) (liveResult, error) {
	return liveSessionWithHints(ctx, engine, rate, inputRate, pcm, nil)
}

func liveSessionWithHints(ctx context.Context, engine string, rate, inputRate int, pcm []byte, hints []string) (liveResult, error) {
	var empty liveResult
	client, err := New(Config{
		AppID:           os.Getenv("TENCENT_APP_ID"),
		SecretID:        os.Getenv("TENCENT_SECRET_ID"),
		SecretKey:       os.Getenv("TENCENT_SECRET_KEY"),
		EngineModel:     engine,
		InputSampleRate: inputRate,
		NeedVAD:         true,
	})
	if err != nil {
		return empty, err
	}
	stream, err := client.OpenStream(ctx, asr.Config{
		Format: audio.Format{
			Encoding: audio.EncodingPCM16LE, SampleRate: rate, Channels: 1,
		},
		Hints: hints,
	})
	if err != nil {
		return empty, fmt.Errorf("Tencent model %s handshake: %w", engine, err)
	}
	defer stream.Close()

	type outcome struct {
		liveResult
		err error
	}
	received := make(chan outcome, 1)
	go func() {
		var out outcome
		for {
			event, err := stream.Recv(ctx)
			if errors.Is(err, io.EOF) {
				received <- out
				return
			}
			if err != nil {
				out.err = err
				received <- out
				return
			}
			switch event.Type {
			case asr.EventPartial:
				out.partials++
			case asr.EventFinal:
				if event.Text != "" {
					out.finals = append(out.finals, event.Text)
				}
			}
		}
	}()

	// Tencent V2 documents 40 ms chunks; the traditional engine uses 100 ms.
	// In both cases maintain 1:1 realtime upload pacing.
	frameMS := 100
	if strings.HasSuffix(engine, "_2.0") {
		frameMS = 40
	}
	chunkSize := rate * 2 * frameMS / 1000
	ticker := time.NewTicker(time.Duration(frameMS) * time.Millisecond)
	defer ticker.Stop()
	for offset := 0; offset < len(pcm); offset += chunkSize {
		if offset > 0 {
			select {
			case <-ticker.C:
			case <-ctx.Done():
				return empty, ctx.Err()
			}
		}
		end := offset + chunkSize
		if end > len(pcm) {
			end = len(pcm)
		}
		if err := stream.Send(ctx, pcm[offset:end]); err != nil {
			return empty, fmt.Errorf("Tencent model %s send: %w", engine, err)
		}
	}
	if err := stream.CloseSend(ctx); err != nil {
		return empty, fmt.Errorf("Tencent model %s close send: %w", engine, err)
	}
	select {
	case out := <-received:
		if out.err != nil {
			return empty, fmt.Errorf("Tencent model %s receive: %w", engine, out.err)
		}
		if len(out.finals) == 0 {
			return empty, fmt.Errorf("Tencent model %s produced no nonempty final", engine)
		}
		for i := 0; i < 2; i++ {
			if _, err := stream.Recv(context.Background()); !errors.Is(err, io.EOF) {
				return empty, fmt.Errorf("Tencent model %s unstable EOF %d: %v", engine, i, err)
			}
		}
		return out.liveResult, nil
	case <-ctx.Done():
		return empty, ctx.Err()
	}
}

// Three simultaneous real Tencent sessions must all finish independently.
func TestTencentLiveConcurrentSessions(t *testing.T) {
	pcm := livePCMFixture(t, "TENCENT_TEST_16K_PCM_FILE")
	ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
	defer cancel()
	const sessions = 3
	errs := make(chan error, sessions)
	var wg sync.WaitGroup
	for i := 0; i < sessions; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := liveSession(ctx, DefaultEngineModel, 16000, 0, pcm)
			if err != nil {
				errs <- err
				return
			}
			if len(result.finals) == 0 {
				errs <- errors.New("concurrent Tencent session had no final")
				return
			}
			errs <- nil
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
}

// A sustained PCM stream must not lose final sentences or EOF after CloseSend.
func TestTencentLiveSustainedPCM(t *testing.T) {
	pcm := livePCMFixture(t, "TENCENT_TEST_16K_PCM_FILE")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	longPCM := make([]byte, 0, 3*len(pcm))
	for i := 0; i < 3; i++ {
		longPCM = append(longPCM, pcm...)
	}
	out, err := liveSession(ctx, DefaultEngineModel, 16000, 0, longPCM)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("sustained Tencent ASR duration=%.2fs, partials=%d, finals=%d",
		float64(len(longPCM))/32000, out.partials, len(out.finals))
}

// Requesting 16k inference with 8k PCM is supported only with Tencent's
// signed input_sample_rate=8000 option; audio passes through unchanged.
func TestTencentLiveEightKInputToSixteenKEngine(t *testing.T) {
	pcm := livePCMFixture(t, "TENCENT_TEST_8K_PCM_FILE")
	ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
	defer cancel()
	out, err := liveSession(ctx, DefaultEngineModel, 8000, 8000, pcm)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Tencent 8k input -> 16k engine partials=%d finals=%d", out.partials, len(out.finals))
}

// This is an opt-in billing/entitlement gate. The legacy 16k_zh account does
// not necessarily have large-model 2.0 access; never treat a skipped test as verified.
func TestTencentLiveLargeModelV2(t *testing.T) {
	model := os.Getenv("TENCENT_V2_ASR_MODEL")
	if model == "" {
		t.Skip("TENCENT_V2_ASR_MODEL not configured; 2.0 cloud interoperability remains unverified")
	}
	if !strings.HasSuffix(model, "_2.0") {
		t.Fatalf("TENCENT_V2_ASR_MODEL must be a 2.0 engine, got %q", model)
	}
	pcm := livePCMFixture(t, "TENCENT_TEST_16K_PCM_FILE")
	ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
	defer cancel()
	out, err := liveSession(ctx, model, 16000, 0, pcm)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Tencent large model 2.0 %s partials=%d finals=%d", model, out.partials, len(out.finals))
}

// Tencent must accept the signed temporary hotword_list during a real PCM
// session. This checks interoperability, not recognition accuracy.
func TestTencentLiveRequestHints(t *testing.T) {
	pcm := livePCMFixture(t, "TENCENT_TEST_16K_PCM_FILE")
	ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
	defer cancel()
	out, err := liveSessionWithHints(ctx, DefaultEngineModel, 16000, 0, pcm, []string{"天气", "游戏"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Tencent request hints partials=%d finals=%d", out.partials, len(out.finals))
}

// TestTencentLiveMinuteStream validates a true >60-second upload without
// changing the SDK's caller-paced model or auto-reopening a WebSocket.
// Tencent recommends VAD to split long utterances; the generated fixture
// contains periodic pauses and uses NeedVAD=true.
func TestTencentLiveMinuteStream(t *testing.T) {
	pcm := livePCMFixture(t, "TENCENT_TEST_16K_PCM_FILE")
	const repetitions = 9 // approximately 62 seconds for the standard CI fixture
	longPCM := make([]byte, 0, repetitions*len(pcm))
	for i := 0; i < repetitions; i++ {
		longPCM = append(longPCM, pcm...)
	}
	duration := time.Duration(len(longPCM)) * time.Second / (16000 * 2)
	if duration < time.Minute {
		t.Fatalf("expected more than one minute of audio, got %v", duration)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	out, err := liveSession(ctx, DefaultEngineModel, 16000, 0, longPCM)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Tencent minute-plus stream duration=%v partials=%d finals=%d",
		duration, out.partials, len(out.finals))
}
