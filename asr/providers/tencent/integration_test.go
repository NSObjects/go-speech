//go:build integration

package tencent

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

// TestTencentLivePCMASR runs only against real Tencent Cloud ASR credentials.
// The caller must supply synthetically generated, realtime-paced PCM16LE.
func TestTencentLivePCMASR(t *testing.T) {
	for _, name := range []string{"TENCENT_APP_ID", "TENCENT_SECRET_ID", "TENCENT_SECRET_KEY", "TENCENT_TEST_PCM_FILE"} {
		if os.Getenv(name) == "" {
			t.Fatalf("%s is required for real Tencent ASR tests", name)
		}
	}
	model := os.Getenv("TENCENT_ASR_MODEL")
	if model == "" {
		model = DefaultEngineModel
	}
	rate := 16000
	if strings.HasPrefix(model, "8k_") {
		rate = 8000
	}
	client, err := New(Config{
		AppID: os.Getenv("TENCENT_APP_ID"), SecretID: os.Getenv("TENCENT_SECRET_ID"),
		SecretKey: os.Getenv("TENCENT_SECRET_KEY"), EngineModel: model, NeedVAD: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(os.Getenv("TENCENT_TEST_PCM_FILE"))
	if err != nil || len(data) < 3200 {
		t.Fatalf("invalid PCM test fixture: length=%d err=%v", len(data), err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()
	s, err := client.OpenStream(ctx, asr.Config{Format: audio.Format{
		Encoding: audio.EncodingPCM16LE, SampleRate: rate, Channels: 1,
	}})
	if err != nil {
		t.Fatalf("Tencent real handshake: %v", err)
	}
	defer s.Close()
	type received struct {
		partials int
		finals   []string
		err      error
	}
	result := make(chan received, 1)
	go func() {
		var r received
		for {
			event, err := s.Recv(ctx)
			if errors.Is(err, io.EOF) {
				result <- r
				return
			}
			if err != nil {
				r.err = err
				result <- r
				return
			}
			switch event.Type {
			case asr.EventPartial:
				r.partials++
			case asr.EventFinal:
				if event.Text != "" {
					r.finals = append(r.finals, event.Text)
				}
			}
		}
	}()
	// Tencent recommends 1:1 audio pacing; send 100 ms of PCM every 100 ms.
	chunkSize := rate * 2 / 10
	for offset := 0; offset < len(data); offset += chunkSize {
		end := offset + chunkSize
		if end > len(data) {
			end = len(data)
		}
		if err := s.Send(ctx, data[offset:end]); err != nil {
			t.Fatalf("Tencent real Send: %v", err)
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	if err := s.CloseSend(ctx); err != nil {
		t.Fatalf("Tencent CloseSend: %v", err)
	}
	select {
	case r := <-result:
		if r.err != nil {
			t.Fatalf("Tencent real Recv: %v", r.err)
		}
		t.Logf("Tencent real model=%s duration=%.2fs partials=%d finals=%q",
			model, float64(len(data))/float64(rate*2), r.partials, r.finals)
		if len(r.finals) == 0 {
			t.Fatal("Tencent ASR returned no final nonempty text")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for i := 0; i < 2; i++ {
		if _, err := s.Recv(context.Background()); !errors.Is(err, io.EOF) {
			t.Fatalf("Recv after successful Tencent recognition %d = %v", i, err)
		}
	}
}
