//go:build integration

package aliyun

import (
	"bytes"
	"context"
	"encoding/binary"
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

// TestAliyunLiveOggPassthroughAndRemux verifies two zero-transcode routes:
// 1. Device already provides Ogg Opus: forward its original bytes to ASR.
// 2. Device provides complete raw Opus packets: OggMuxer adds only Ogg pages.
//
// Both routes send "format=opus" and never call a PCM decoder.
func TestAliyunLiveOggPassthroughAndRemux(t *testing.T) {
	if os.Getenv("DASHSCOPE_ASR_MODEL") != ModelFunASRRealtime {
		t.Skip("direct Ogg Opus integration is scoped to Fun-ASR")
	}
	apiKey := os.Getenv("DASHSCOPE_API_KEY")
	endpoint := os.Getenv("DASHSCOPE_WS_ENDPOINT")
	path := os.Getenv("ASR_TEST_OPUS_FILE")
	if apiKey == "" || endpoint == "" || path == "" {
		t.Fatal("DASHSCOPE_API_KEY, DASHSCOPE_WS_ENDPOINT and ASR_TEST_OPUS_FILE are required")
	}
	ogg, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if enc, err := audio.Detect(ogg); err != nil || enc != audio.EncodingOpus {
		t.Fatalf("test fixture is not Ogg Opus: %q, %v", enc, err)
	}
	packets, err := extractRawOpusPackets(ogg)
	if err != nil {
		t.Fatal(err)
	}
	if len(ogg) < 28 {
		t.Fatal("truncated Ogg header")
	}
	segments := int(ogg[26])
	headAt := 27+segments
	if len(ogg) < headAt+19 || !bytes.Equal(ogg[headAt:headAt+8], []byte("OpusHead")) {
		t.Fatal("missing OpusHead in test fixture")
	}
	preSkip := binary.LittleEndian.Uint16(ogg[headAt+10:headAt+12])
	sourceRate := int(binary.LittleEndian.Uint32(ogg[headAt+12:headAt+16]))
	if sourceRate == 0 {
		sourceRate = 48000
	}
	channels := int(ogg[headAt+9])
	client, err := New(Config{
		Endpoint: endpoint,
		APIKey: apiKey,
		Model: ModelFunASRRealtime,
	})
	if err != nil {
		t.Fatal(err)
	}

	run := func(t *testing.T, send func(context.Context, asr.Stream) error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
		defer cancel()
		session, err := client.OpenStream(ctx, asr.Config{
			Format: audio.Format{Encoding: audio.EncodingOpus, SampleRate: sourceRate, Channels: 1},
		})
		if err != nil {
			t.Fatalf("open Opus ASR task: %v", err)
		}
		defer session.Close()
		type result struct {
			text string
			err error
		}
		received := make(chan result, 1)
		go func() {
			var finals []string
			for {
				event, recvErr := session.Recv(ctx)
				if errors.Is(recvErr, io.EOF) {
					received <- result{text: strings.Join(finals, " ")}
					return
				}
				if recvErr != nil {
					received <- result{err: recvErr}
					return
				}
				if event.Type == asr.EventFinal {
					finals = append(finals, event.Text)
				}
			}
		}()
		if err := send(ctx, session); err != nil {
			t.Fatalf("send Opus audio: %v", err)
		}
		if err := session.CloseSend(ctx); err != nil {
			t.Fatalf("finish task: %v", err)
		}
		select {
		case result := <-received:
			if result.err != nil {
				t.Fatalf("receive ASR result: %v", result.err)
			}
			if strings.TrimSpace(result.text) == "" {
				t.Fatal("Ogg Opus direct route returned no final transcript")
			}
			t.Logf("zero-transcode Ogg Opus ASR transcript: %q", result.text)
		case <-ctx.Done():
			t.Fatalf("timed out waiting for final transcript: %v", ctx.Err())
		}
	}

	t.Run("ogg-passthrough", func(t *testing.T) {
		run(t, func(ctx context.Context, stream asr.Stream) error {
			// Deliberately split Ogg pages over binary WebSocket messages:
			// service must receive bytes as one continuous Ogg logical stream.
			for offset := 0; offset < len(ogg); {
				end := offset+320
				if end > len(ogg) {
					end = len(ogg)
				}
				if err := stream.Send(ctx, ogg[offset:end]); err != nil {
					return err
				}
				offset = end
				select {
				case <-time.After(80 * time.Millisecond):
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		})
	})
	t.Run("raw-packet-ogg-remux", func(t *testing.T) {
		run(t, func(ctx context.Context, stream asr.Stream) error {
			muxer, err := opus.NewOggMuxer(opus.OggConfig{
				Channels: channels, InputSampleRate: sourceRate, PreSkip: preSkip,
			})
			if err != nil {
				return err
			}
			headers, err := muxer.Headers()
			if err != nil {
				return err
			}
			if err := stream.Send(ctx, headers); err != nil {
				return err
			}
			for _, packet := range packets {
				page, err := muxer.WritePacket(packet)
				if err != nil {
					return err
				}
				if err := stream.Send(ctx, page); err != nil {
					return err
				}
				// Fixture uses FFmpeg's 20ms default packet duration.
				select {
				case <-time.After(20 * time.Millisecond):
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			eos, err := muxer.Close()
			if err != nil {
				return err
			}
			return stream.Send(ctx, eos)
		})
	})
}
