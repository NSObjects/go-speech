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

// TestVolcengineLiveFLEURSMandarinCanary checks two independently annotated,
// real Mandarin recordings from FLEURS (CC BY 4.0). It does not replace the
// existing strict multi-turn tests until both samples prove intelligible.
// The pinned WAVs are hash-verified and converted to PCM by the CI workflow.
func TestVolcengineLiveFLEURSMandarinCanary(t *testing.T) {
	key := os.Getenv("VOLCENGINE_API_KEY")
	if key == "" {
		t.Fatal("VOLCENGINE_API_KEY is required")
	}
	cases := []struct {
		name    string
		envPath string
		want    []string
	}{
		{
			name:    "fleurs-zh-short-1692",
			envPath: "VOLCENGINE_FLEURS_ZH_SHORT_PCM_FILE",
			// google/fleurs cmn_hans_cn test.tsv, ID 17639585860488007329
			want: []string{"核电站", "放射性"},
		},
		{
			name:    "fleurs-zh-1906",
			envPath: "VOLCENGINE_FLEURS_ZH_1906_PCM_FILE",
			// google/fleurs cmn_hans_cn test.tsv, ID 1906
			want: []string{"告别", "篇章"},
		},
	}

	format := audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := os.Getenv(tc.envPath)
			if path == "" {
				t.Fatalf("%s is required", tc.envPath)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := audio.ValidateBuffer(audio.Buffer{Data: raw, Format: format}); err != nil {
				t.Fatalf("invalid pinned natural Mandarin PCM: %v", err)
			}
			if len(raw) < 16000*2*4 {
				t.Fatalf("unexpected short candidate: %d bytes", len(raw))
			}
			// End with silence so streaming VAD has time to finalize speech.
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
					ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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

					const frameBytes = 16000 * 2 / 5 // 200ms PCM16LE, mono
					for offset := 0; offset < len(raw); offset += frameBytes {
						end := offset + frameBytes
						if end > len(raw) {
							end = len(raw)
						}
						if err := stream.Send(ctx, raw[offset:end]); err != nil {
							t.Fatalf("stream candidate PCM at %d: %v", offset, err)
						}
						select {
						case <-time.After(200 * time.Millisecond):
						case <-ctx.Done():
							t.Fatalf("stream candidate PCM: %v", ctx.Err())
						}
					}
					if err := stream.CloseSend(ctx); err != nil {
						t.Fatalf("close candidate PCM input: %v", err)
					}
					select {
					case result := <-done:
						if result.err != nil {
							t.Fatalf("receive candidate: %v", result.err)
						}
						transcript := strings.Join(result.finals, "")
						t.Logf("FLEURS Mandarin candidate=%s mode=%s finals=%q", tc.name, mode, result.finals)
						for _, expected := range tc.want {
							if !strings.Contains(transcript, expected) {
								t.Fatalf("FLEURS reference keyword %q missing: %q", expected, transcript)
							}
						}
						for i := 0; i < 2; i++ {
							if _, err := stream.Recv(context.Background()); !errors.Is(err, io.EOF) {
								t.Fatalf("unstable EOF on read %d: %v", i, err)
							}
						}
					case <-ctx.Done():
						t.Fatalf("candidate recording did not finalize: %v", ctx.Err())
					}
				})
			}
		})
	}
}
