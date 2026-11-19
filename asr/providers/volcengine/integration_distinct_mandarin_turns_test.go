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

// TestVolcengineLiveDistinctMandarinTurns verifies actual turn segmentation,
// not merely repeated text from one natural Mandarin fixture. The first and
// third utterances are an official Tencent SDK natural recording; the second
// and fourth are a distinct FLEURS human recording (CC BY 4.0). CI pins
// source commits and Git blob hashes, without embedding WAV/PCM in this repo.
func TestVolcengineLiveDistinctMandarinTurns(t *testing.T) {
	key := os.Getenv("VOLCENGINE_API_KEY")
	tencentPath := os.Getenv("VOLCENGINE_OFFICIAL_MANDARIN_PCM_FILE")
	fleursPath := os.Getenv("VOLCENGINE_FLEURS_ZH_SHORT_PCM_FILE")
	if key == "" || tencentPath == "" || fleursPath == "" {
		t.Fatal("VOLCENGINE_API_KEY, VOLCENGINE_OFFICIAL_MANDARIN_PCM_FILE and VOLCENGINE_FLEURS_ZH_SHORT_PCM_FILE are required")
	}

	official, err := os.ReadFile(tencentPath)
	if err != nil {
		t.Fatal(err)
	}
	fleurs, err := os.ReadFile(fleursPath)
	if err != nil {
		t.Fatal(err)
	}
	format := audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1}
	for _, item := range []struct {
		name string
		data []byte
	}{{"official", official}, {"fleurs", fleurs}} {
		if err := audio.ValidateBuffer(audio.Buffer{Data: item.data, Format: format}); err != nil {
			t.Fatalf("%s is not valid PCM16LE: %v", item.name, err)
		}
	}
	if len(official) != 76360 {
		t.Fatalf("unexpected official PCM size: %d", len(official))
	}
	if len(fleurs) < 16000*2*9 || len(fleurs) > 16000*2*13 {
		t.Fatalf("unexpected FLEURS sample duration: %.2fs", float64(len(fleurs))/32000)
	}

	const bytesPerSecond = 16000 * 2
	gap := make([]byte, 2*bytesPerSecond)
	tail := make([]byte, 3*bytesPerSecond/2)
	turns := [][]byte{official, fleurs, official, fleurs}
	want := []string{"腾讯云", "核电站", "腾讯云", "核电站"}
	total := len(tail) + len(gap)*3
	for _, utterance := range turns {
		total += len(utterance)
	}
	pcm := make([]byte, 0, total)
	for i, utterance := range turns {
		pcm = append(pcm, utterance...)
		if i < len(turns)-1 {
			pcm = append(pcm, gap...)
		}
	}
	pcm = append(pcm, tail...)
	if len(pcm) != total {
		t.Fatalf("fixture construction lost samples: got %d, expected %d", len(pcm), total)
	}

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
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
			defer cancel()
			stream, err := client.OpenStream(ctx, asr.Config{Format: format})
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()

			type final struct {
				text string
				at   time.Time
			}
			type outcome struct {
				finals []final
				err    error
			}
			done := make(chan outcome, 1)
			started := time.Now()
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
						result.finals = append(result.finals, final{text: ev.Text, at: time.Now()})
					}
				}
			}()

			const frameBytes = bytesPerSecond / 5 // 200ms real-time pacing
			for offset := 0; offset < len(pcm); offset += frameBytes {
				end := offset + frameBytes
				if end > len(pcm) {
					end = len(pcm)
				}
				if err := stream.Send(ctx, pcm[offset:end]); err != nil {
					t.Fatalf("send mixed natural Mandarin offset %d: %v", offset, err)
				}
				select {
				case <-time.After(200 * time.Millisecond):
				case <-ctx.Done():
					t.Fatalf("mixed natural Mandarin pacing: %v", ctx.Err())
				}
			}
			closedInputAt := time.Now()
			if err := stream.CloseSend(ctx); err != nil {
				t.Fatalf("finish mixed natural Mandarin: %v", err)
			}
			select {
			case got := <-done:
				if got.err != nil {
					t.Fatalf("read mixed natural Mandarin: %v", got.err)
				}
				texts := make([]string, 0, len(got.finals))
				beforeClose := 0
				for _, f := range got.finals {
					texts = append(texts, f.text)
					if f.at.Before(closedInputAt) {
						beforeClose++
					}
				}
				t.Logf("distinct Mandarin mode=%s audio=%.2fs wall=%.2fs finals_before_end=%d finals=%q",
					mode, float64(len(pcm))/bytesPerSecond, time.Since(started).Seconds(), beforeClose, texts)
				if beforeClose < 3 {
					t.Fatalf("expected at least 3 finalized turns while sending, got %d: %q", beforeClose, texts)
				}
				if len(texts) != len(want) {
					t.Fatalf("expected %d independently finalized turns, got %d: %q", len(want), len(texts), texts)
				}
				for i, expected := range want {
					if !strings.Contains(texts[i], expected) {
						t.Fatalf("turn %d missing/reordered natural Mandarin keyword %q: %q", i+1, expected, texts)
					}
				}
				for i := 0; i < 2; i++ {
					if _, err := stream.Recv(context.Background()); !errors.Is(err, io.EOF) {
						t.Fatalf("unstable EOF after mixed turns (read %d): %v", i, err)
					}
				}
			case <-ctx.Done():
				t.Fatalf("mixed Mandarin stream did not complete: %v", ctx.Err())
			}
		})
	}
}
