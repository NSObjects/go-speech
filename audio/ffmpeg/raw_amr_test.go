package ffmpeg

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/audio/codec/amrnb"
)

// Exercise the pure-Go AMR-NB storage-frame framer with a long-lived FFmpeg
// decoder. The device sends only per-frame bytes, not a #!AMR file header.
func TestAMRNBRawFrameFallback(t *testing.T) {
	binaryName := checkFFmpeg(t)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	stream, err := OpenStreamDecoder(ctx, StreamConfig{
		Input:  audio.Format{Encoding: audio.EncodingAMR, SampleRate: 8000, Channels: 1},
		Output: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
		Binary: binaryName,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	framer, err := amrnb.NewFramer(stream)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 4096)
		total := 0
		for {
			n, readErr := stream.Read(ctx, buf)
			total += n
			if errors.Is(readErr, io.EOF) {
				if total < 16000 {
					done <- errors.New("too little decoded AMR PCM")
				} else {
					done <- nil
				}
				return
			}
			if readErr != nil {
				done <- readErr
				return
			}
		}
	}()
	for i := 0; i < 100; i++ {
		frame := make([]byte, 32)
		frame[0] = 0x3c // FT=7; AMR-NB 12.2kbps frame + valid Q flag.
		if err := framer.WriteFrame(ctx, frame); err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
	}
	if err := framer.CloseInput(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("AMR raw-frame stream failed to terminate", ctx.Err())
	}
}
