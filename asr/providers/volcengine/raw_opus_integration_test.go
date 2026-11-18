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
	"github.com/NSObjects/go-speech/audio/codec/opus"
)

// Tests the SDK's Go OggMuxer fed complete raw Opus packets, not FFmpeg Ogg
// pages directly. The audio is synthesized; physical hardware is untested.
func TestVolcengineLiveRawOpusRemux(t *testing.T) {
	key, path := os.Getenv("VOLCENGINE_API_KEY"), os.Getenv("VOLCENGINE_CONTINUOUS_OGG_OPUS_FILE")
	if key == "" || path == "" {
		t.Fatal("Volcengine key and continuous Ogg fixture are required")
	}
	ogg, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	source, err := extractFixtureRawOpus(ogg)
	if err != nil {
		t.Fatal(err)
	}
	if len(source.packets) < 100 {
		t.Fatalf("too few Opus packets: %d", len(source.packets))
	}
	m, err := opus.NewOggMuxer(source.config)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		APIKey: key, ResourceID: os.Getenv("VOLCENGINE_RESOURCE_ID"),
		ResultType: ResultSingle, EnableNonStream: true, EndWindowSize: 700 * time.Millisecond,
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
		Format: audio.Format{Encoding: audio.EncodingOpus, SampleRate: 16000, Channels: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	type final struct {
		text string
		at   time.Time
	}
	type outcome struct {
		finals   []final
		partials int
		err      error
	}
	done := make(chan outcome, 1)
	go func() {
		var got outcome
		for {
			ev, err := stream.Recv(ctx)
			if errors.Is(err, io.EOF) {
				done <- got
				return
			}
			if err != nil {
				got.err = err
				done <- got
				return
			}
			switch ev.Type {
			case asr.EventPartial:
				got.partials++
			case asr.EventFinal:
				got.finals = append(got.finals, final{text: ev.Text, at: time.Now()})
			}
		}
	}()

	headers, err := m.Headers()
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(ctx, headers); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	batch := make([]byte, 0, 8192)
	batches := 0
	for i, packet := range source.packets {
		page, err := m.WritePacket(packet)
		if err != nil {
			t.Fatalf("mux packet %d: %v", i, err)
		}
		batch = append(batch, page...)
		if (i+1)%10 != 0 && i != len(source.packets)-1 {
			continue
		}
		// Each codec packet yields one Ogg page. Batch ~200ms before sending
		// without touching compressed packets, headers, or Ogg CRCs.
		if err := stream.Send(ctx, batch); err != nil {
			t.Fatalf("send batch %d: %v", batches, err)
		}
		batches++
		batch = batch[:0]
		target := started.Add(time.Duration(m.GranulePosition()) * time.Second / 48000)
		if wait := time.Until(target); wait > 0 {
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
	}
	eos, err := m.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(ctx, eos); err != nil {
		t.Fatalf("send Ogg EOS: %v", err)
	}
	beforeClose := time.Now()
	if err := stream.CloseSend(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("raw Opus remux recognition: %v", got.err)
		}
		var texts []string
		early := 0
		for _, f := range got.finals {
			texts = append(texts, f.text)
			if f.at.Before(beforeClose) {
				early++
			}
		}
		joined := strings.Join(texts, "")
		t.Logf("raw Opus packets=%d 200ms batches=%d duration=%.2fs partials=%d finals_before_close=%d text=%q",
			len(source.packets), batches, time.Since(started).Seconds(), got.partials, early, texts)
		if early < 1 || len(texts) != 4 {
			t.Fatalf("incomplete streaming result: early=%d texts=%q", early, texts)
		}
		offset := 0
		for _, word := range []string{"天气", "游戏", "天气", "游戏"} {
			at := strings.Index(joined[offset:], word)
			if at < 0 {
				t.Fatalf("missing or reordered keyword %q in %q", word, joined)
			}
			offset += at + len(word)
		}
		for _, word := range []string{"天气", "游戏"} {
			if strings.Count(joined, word) != 2 {
				t.Fatalf("unexpected count for %q in %q", word, joined)
			}
		}
		for i := 0; i < 2; i++ {
			if _, err := stream.Recv(context.Background()); !errors.Is(err, io.EOF) {
				t.Fatalf("EOF check %d: %v", i, err)
			}
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
