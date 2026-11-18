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

// TestVolcengineLiveContinuousOggOpus verifies that confirmed results are
// delivered while one continuous Ogg Opus stream is still sending audio,
// not only after CloseSend. EventPartial counts are diagnostic: a short
// synthetic voice fixture may not yield partial hypotheses on every run.
func TestVolcengineLiveContinuousOggOpus(t *testing.T) {
	key := os.Getenv("VOLCENGINE_API_KEY")
	path := os.Getenv("VOLCENGINE_CONTINUOUS_OGG_OPUS_FILE")
	if key == "" || path == "" {
		t.Fatal("VOLCENGINE_API_KEY and VOLCENGINE_CONTINUOUS_OGG_OPUS_FILE are required")
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pages, err := splitOggPages(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if last := pages[len(pages)-1].granule; last < 48000*12 {
		t.Fatalf("expected at least 12 seconds of Ogg Opus, final granule=%d", last)
	}
	for _, resultType := range []string{ResultSingle, ResultFull} {
		t.Run(resultType, func(t *testing.T) {
			cfg := Config{
				APIKey:          key,
				ResourceID:      os.Getenv("VOLCENGINE_RESOURCE_ID"),
				ResultType:      resultType,
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
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			stream, err := client.OpenStream(ctx, asr.Config{
				Format: audio.Format{
					Encoding:   audio.EncodingOpus,
					SampleRate: 16000,
					Channels:   1,
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()

			type observation struct {
				text     string
				received time.Time
			}
			type outcome struct {
				finals       []observation
				partials     int
				firstPartial time.Time
				err          error
			}
			done := make(chan outcome, 1)
			started := time.Now()
			go func() {
				result := outcome{}
				for {
					event, err := stream.Recv(ctx)
					if errors.Is(err, io.EOF) {
						done <- result
						return
					}
					if err != nil {
						result.err = err
						done <- result
						return
					}
					receivedAt := time.Now()
					switch event.Type {
					case asr.EventPartial:
						result.partials++
						if result.firstPartial.IsZero() {
							result.firstPartial = receivedAt
						}
					case asr.EventFinal:
						if strings.TrimSpace(event.Text) != "" {
							result.finals = append(result.finals, observation{
								text: event.Text, received: receivedAt,
							})
						}
					}
				}
			}()

			for i, page := range pages {
				if err := stream.Send(ctx, page.data); err != nil {
					t.Fatalf("send Ogg page %d/%d: %v", i+1, len(pages), err)
				}
				if page.granule == ^uint64(0) {
					continue // an Ogg page may carry no complete audio packets
				}
				if page.granule > 48000*60 {
					t.Fatalf("unreasonable Ogg granule at page %d: %d", i+1, page.granule)
				}
				target := started.Add(time.Duration(page.granule) * time.Second / 48000)
				if remaining := time.Until(target); remaining > 0 {
					select {
					case <-time.After(remaining):
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
				}
			}
			closeSendAt := time.Now()
			if err := stream.CloseSend(ctx); err != nil {
				t.Fatalf("CloseSend after all Ogg pages: %v", err)
			}
			select {
			case got := <-done:
				if got.err != nil {
					t.Fatalf("continuous real Ogg Opus ASR: %v", got.err)
				}
				texts := make([]string, 0, len(got.finals))
				confirmedBeforeEnd := 0
				for _, final := range got.finals {
					texts = append(texts, final.text)
					if final.received.Before(closeSendAt) {
						confirmedBeforeEnd++
					}
				}
				firstPartial := "none"
				if !got.firstPartial.IsZero() {
					firstPartial = got.firstPartial.Sub(started).String()
				}
				firstFinal := "none"
				if len(got.finals) > 0 {
					firstFinal = got.finals[0].received.Sub(started).String()
				}
				t.Logf("continuous Ogg Opus mode=%s pages=%d bytes=%d elapsed=%.2fs partials=%d first_partial=%s first_final=%s finals_before_close=%d finals=%q",
					resultType, len(pages), len(encoded), time.Since(started).Seconds(),
					got.partials, firstPartial, firstFinal, confirmedBeforeEnd, texts)
				if confirmedBeforeEnd < 1 {
					t.Fatalf("no confirmed recognition while Ogg Opus input was still streaming: %q", texts)
				}
				if len(texts) < 4 {
					t.Fatalf("expected four confirmed Mandarin phrases, got %d: %q", len(texts), texts)
				}
				joined := strings.Join(texts, "")
				offset := 0
				for _, word := range []string{"天气", "游戏", "天气", "游戏"} {
					at := strings.Index(joined[offset:], word)
					if at < 0 {
						t.Fatalf("missing or reordered spoken phrase %q: %q", word, joined)
					}
					offset += at + len(word)
				}
				for _, word := range []string{"天气", "游戏"} {
					if n := strings.Count(joined, word); n != 2 {
						t.Fatalf("Ogg Opus duplicated/dropped %q: count=%d, transcript=%q", word, n, joined)
					}
				}
				for i := 0; i < 2; i++ {
					if _, err := stream.Recv(context.Background()); !errors.Is(err, io.EOF) {
						t.Fatalf("unstable Ogg Opus EOF on call %d: %v", i, err)
					}
				}
			case <-ctx.Done():
				t.Fatalf("waiting for Ogg Opus stream completion: %v", ctx.Err())
			}
		})
	}
}
