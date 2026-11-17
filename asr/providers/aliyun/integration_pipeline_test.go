//go:build integration

package aliyun

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/audio/ffmpeg"
)

// TestAliyunLiveOpusPipeline exercises the actual layered audio path:
//
//	Ogg Opus fragments -> one persistent decoder -> PCM16LE -> Aliyun ASR.
//
// Unlike TestAliyunLiveASR, this test also checks that output audio is drained
// from the decoder concurrently with input writes. It is automatically run
// only for Fun-ASR in the paid GitHub workflow.
func TestAliyunLiveOpusPipeline(t *testing.T) {
	if os.Getenv("DASHSCOPE_ASR_MODEL") != ModelFunASRRealtime {
		t.Skip("live Ogg Opus chain smoke test is scoped to Fun-ASR")
	}
	key := os.Getenv("DASHSCOPE_API_KEY")
	endpoint := os.Getenv("DASHSCOPE_WS_ENDPOINT")
	path := os.Getenv("ASR_TEST_OPUS_FILE")
	if key == "" || endpoint == "" || path == "" {
		t.Fatal("missing DASHSCOPE_API_KEY, DASHSCOPE_WS_ENDPOINT, or ASR_TEST_OPUS_FILE")
	}
	ogg, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if enc, err := audio.Detect(ogg); err != nil || enc != audio.EncodingOpus {
		t.Fatalf("expected Ogg Opus fixture: encoding %q, error %v", enc, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()

	const rate = 16000
	client, err := New(Config{Endpoint: endpoint, APIKey: key, Model: ModelFunASRRealtime})
	if err != nil {
		t.Fatal(err)
	}
	recognizer, err := client.OpenStream(ctx, asr.Config{
		Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: rate, Channels: 1},
	})
	if err != nil {
		t.Fatalf("connect ASR: %v", err)
	}
	defer recognizer.Close()

	decoder, err := ffmpeg.OpenStreamDecoder(ctx, ffmpeg.StreamConfig{
		Input:  audio.Format{Encoding: audio.EncodingOpus, SampleRate: 48000, Channels: 1},
		Output: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: rate, Channels: 1},
	})
	if err != nil {
		t.Fatalf("open Opus decoder: %v", err)
	}
	defer decoder.Close()

	type transcript struct {
		text string
		err  error
	}
	results := make(chan transcript, 1)
	go func() {
		var finals []string
		for {
			ev, err := recognizer.Recv(ctx)
			if errors.Is(err, io.EOF) {
				results <- transcript{text: strings.Join(finals, " ")}
				return
			}
			if err != nil {
				results <- transcript{err: err}
				return
			}
			if ev.Type == asr.EventFinal {
				finals = append(finals, ev.Text)
			}
		}
	}()

	sentPCM := make(chan error, 1)
	go func() {
		const chunkBytes = rate * 2 / 10
		buffer := make([]byte, 3200)
		var pending []byte
		var total int
		send := func(pcm []byte) error {
			if err := recognizer.Send(ctx, pcm); err != nil {
				return err
			}
			total += len(pcm)
			select {
			case <-time.After(100 * time.Millisecond):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		for {
			n, readErr := decoder.Read(ctx, buffer)
			if n > 0 {
				pending = append(pending, buffer[:n]...)
				for len(pending) >= chunkBytes {
					if err := send(pending[:chunkBytes]); err != nil {
						sentPCM <- fmt.Errorf("send decoded PCM: %w", err)
						return
					}
					pending = pending[chunkBytes:]
				}
			}
			if errors.Is(readErr, io.EOF) {
				if len(pending)%2 != 0 {
					sentPCM <- fmt.Errorf("decoded PCM has a truncated sample")
					return
				}
				if len(pending) != 0 {
					if err := send(pending); err != nil {
						sentPCM <- err
						return
					}
				}
				if total < rate*2 {
					sentPCM <- fmt.Errorf("decoder emitted too little speech: %d bytes", total)
					return
				}
				sentPCM <- recognizer.CloseSend(ctx)
				return
			}
			if readErr != nil {
				sentPCM <- fmt.Errorf("decode Ogg Opus: %w", readErr)
				return
			}
		}
	}()

	// The test fixture is a single Ogg logical bitstream; intentionally split
	// it across arbitrary input boundaries to exercise decoder state.
	for offset := 0; offset < len(ogg); {
		end := offset + 113 + offset%277
		if end > len(ogg) {
			end = len(ogg)
		}
		n, err := decoder.Write(ctx, ogg[offset:end])
		if err != nil {
			t.Fatalf("write compressed Ogg at byte %d: %v", offset, err)
		}
		if n <= 0 {
			t.Fatal("Opus decoder input stalled")
		}
		offset += n
	}
	if err := decoder.CloseInput(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-sentPCM:
		if err != nil {
			t.Fatalf("decoded PCM delivery: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("timeout waiting for decoded audio: %v", ctx.Err())
	}
	select {
	case result := <-results:
		if result.err != nil {
			t.Fatalf("live ASR receive: %v", result.err)
		}
		if strings.TrimSpace(result.text) == "" {
			t.Fatal("decoded Ogg Opus ASR pipeline produced no final transcript")
		}
		t.Logf("Ogg Opus -> FFmpeg -> PCM16LE -> Fun-ASR recognized: %q", result.text)
	case <-ctx.Done():
		t.Fatalf("timeout waiting for final ASR text: %v", ctx.Err())
	}
}
