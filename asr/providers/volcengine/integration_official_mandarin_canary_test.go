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

// TestVolcengineLiveOfficialMandarinCanary isolates fixture intelligibility
// from transport behavior. The pinned, natural Mandarin speech recording
// comes from TencentCloud/tencentcloud-speech-sdk-go's public example.
// The same audio produces "腾讯云智能语音欢迎您" in this repository's
// independently verified Tencent Cloud live ASR tests.
//
// The fixture is fetched and SHA-verified by the separate paid CI job;
// the SDK never downloads audio or changes providers to run this test.
func TestVolcengineLiveOfficialMandarinCanary(t *testing.T) {
	key := os.Getenv("VOLCENGINE_API_KEY")
	path := os.Getenv("VOLCENGINE_OFFICIAL_MANDARIN_PCM_FILE")
	if key == "" || path == "" {
		t.Fatal("VOLCENGINE_API_KEY and VOLCENGINE_OFFICIAL_MANDARIN_PCM_FILE are required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const expectedBytes = 76360
	if len(raw) != expectedBytes {
		t.Fatalf("unexpected official sample size: got %d, want %d", len(raw), expectedBytes)
	}
	format := audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1}
	if err := audio.ValidateBuffer(audio.Buffer{Data: raw, Format: format}); err != nil {
		t.Fatalf("official sample must be valid 16 kHz PCM16LE: %v", err)
	}
	// Append 1.5s of silence so cloud VAD can commit a Final before EOS.
	raw = append(raw, make([]byte, 16000*2*3/2)...)

	for _, mode := range []string{ResultSingle, ResultFull} {
		t.Run(mode, func(t *testing.T) {
			cfg := Config{
				APIKey:          key,
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
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			stream, err := client.OpenStream(ctx, asr.Config{Format: format})
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()

			type outcome struct {
				finals []string
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				var result outcome
				for {
					ev, err := stream.Recv(ctx)
					if errors.Is(err, io.EOF) {
						done <- result
						return
					}
					if err != nil {
						result.err = err
						done <- result
						return
					}
					if ev.Type == asr.EventFinal && strings.TrimSpace(ev.Text) != "" {
						result.finals = append(result.finals, ev.Text)
					}
				}
			}()

			const frameBytes = 16000 * 2 / 5 // 200ms signed 16-bit mono PCM
			for offset := 0; offset < len(raw); offset += frameBytes {
				end := offset + frameBytes
				if end > len(raw) {
					end = len(raw)
				}
				if err := stream.Send(ctx, raw[offset:end]); err != nil {
					t.Fatalf("send official Mandarin PCM at %d: %v", offset, err)
				}
				select {
				case <-time.After(200 * time.Millisecond):
				case <-ctx.Done():
					t.Fatalf("pacing official Mandarin PCM: %v", ctx.Err())
				}
			}
			if err := stream.CloseSend(ctx); err != nil {
				t.Fatalf("finish official Mandarin PCM: %v", err)
			}
			select {
			case got := <-done:
				if got.err != nil {
					t.Fatalf("receive official Mandarin transcript: %v", got.err)
				}
				text := strings.Join(got.finals, "")
				t.Logf("official Mandarin mode=%s finals=%q", mode, got.finals)
				if !strings.Contains(text, "腾讯云") {
					t.Fatalf("natural Mandarin sample did not produce confirmed 腾讯云: %q", text)
				}
				if _, err := stream.Recv(context.Background()); !errors.Is(err, io.EOF) {
					t.Fatalf("unstable EOF after official Mandarin sample: %v", err)
				}
			case <-ctx.Done():
				t.Fatalf("official Mandarin sample did not finish: %v", ctx.Err())
			}
		})
	}
}
