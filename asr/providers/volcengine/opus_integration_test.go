//go:build integration

package volcengine

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
)

// oggPage is a complete, consecutive Ogg page from one logical Opus stream.
// Opus granule positions use a 48kHz timeline even when the input is 16kHz.
type oggPage struct {
	data    []byte
	granule uint64
}

// Split pages for transport without assuming a constant encoded byte rate.
// Sending arbitrary raw Opus packets as format=ogg is not valid.
func splitOggPages(data []byte) ([]oggPage, error) {
	var pages []oggPage
	for offset := 0; offset < len(data); {
		if len(data)-offset < 27 || !bytes.Equal(data[offset:offset+4], []byte("OggS")) ||
			data[offset+4] != 0 {
			return nil, fmt.Errorf("invalid Ogg page header at byte %d", offset)
		}
		segments := int(data[offset+26])
		if len(data)-offset < 27+segments {
			return nil, fmt.Errorf("truncated Ogg segment table at byte %d", offset)
		}
		size := 27 + segments
		for _, segmentLen := range data[offset+27 : offset+27+segments] {
			size += int(segmentLen)
		}
		if len(data)-offset < size {
			return nil, fmt.Errorf("truncated Ogg payload at byte %d", offset)
		}
		pages = append(pages, oggPage{
			data: data[offset : offset+size],
			granule: binary.LittleEndian.Uint64(data[offset+6 : offset+14]),
		})
		offset += size
	}
	if len(pages) < 3 || !bytes.Contains(pages[0].data, []byte("OpusHead")) {
		return nil, errors.New("Ogg Opus stream is missing codec headers or audio pages")
	}
	return pages, nil
}

// TestVolcengineLiveOggOpus validates an actual billable optimized streaming
// ASR session using Ogg Opus, rather than a PCM test or a mock encoding header.
// The waveform is synthetic Mandarin; real device captures remain untested.
func TestVolcengineLiveOggOpus(t *testing.T) {
	key := os.Getenv("VOLCENGINE_API_KEY")
	path := os.Getenv("VOLCENGINE_OGG_OPUS_FILE")
	if key == "" || path == "" {
		t.Fatal("VOLCENGINE_API_KEY and VOLCENGINE_OGG_OPUS_FILE are required")
	}
	ogg, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pages, err := splitOggPages(ogg)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		APIKey:          key,
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
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	stream, err := client.OpenStream(ctx, asr.Config{
		Format: audio.Format{Encoding: audio.EncodingOpus, SampleRate: 16000, Channels: 1},
	})
	if err != nil {
		t.Fatalf("open Ogg Opus ASR stream: %v", err)
	}
	defer stream.Close()

	type outcome struct {
		finals    []string
		partials int
		err      error
	}
	done := make(chan outcome, 1)
	go func() {
		var got outcome
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
			if event.Type == asr.EventPartial {
				got.partials++
			}
			if event.Type == asr.EventFinal {
				got.finals = append(got.finals, event.Text)
			}
		}
	}()

	// On the wire an Ogg page is a contiguous piece of the same stream.
	// Pace based on the Opus 48kHz granule timeline rather than file bytes.
	started := time.Now()
	for i, page := range pages {
		if err := stream.Send(ctx, page.data); err != nil {
			t.Fatalf("send Ogg page %d/%d: %v", i+1, len(pages), err)
		}
		if page.granule == ^uint64(0) {
			continue
		}
		if page.granule > 48000*30 {
			t.Fatalf("unexpected Ogg granule at page %d: %d", i+1, page.granule)
		}
		target := started.Add(time.Duration(page.granule) * time.Second / 48000)
		if remaining := time.Until(target); remaining > 0 {
			select {
			case <-time.After(remaining):
			case <-ctx.Done():
				t.Fatalf("streaming Ogg audio timed out: %v", ctx.Err())
			}
		}
	}
	if err := stream.CloseSend(ctx); err != nil {
		t.Fatalf("close Ogg audio input: %v", err)
	}
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("real Ogg Opus recognition: %v", got.err)
		}
		text := strings.Join(got.finals, "")
		t.Logf("Ogg Opus pages=%d bytes=%d wall=%.2fs finals=%q partials=%d",
			len(pages), len(ogg), time.Since(started).Seconds(), got.finals, got.partials)
		if !strings.Contains(text, "天气") {
			t.Fatalf("expected confirmed Mandarin '天气' from real Ogg Opus, got %q", text)
		}
		if strings.Count(text, "天气") != 1 {
			t.Fatalf("unexpected duplicate Ogg Opus transcript %q", text)
		}
		for i := 0; i < 2; i++ {
			if _, err := stream.Recv(context.Background()); !errors.Is(err, io.EOF) {
				t.Fatalf("unstable Ogg Opus EOF on read %d: %v", i, err)
			}
		}
	case <-ctx.Done():
		t.Fatalf("waiting for Ogg Opus final: %v", ctx.Err())
	}
}
