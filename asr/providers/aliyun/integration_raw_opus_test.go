//go:build integration && libopus && cgo

package aliyun

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

// TestAliyunLiveRawOpusPipeline verifies the device-oriented path:
//
//	complete raw Opus packets -> libopus PacketDecoder -> PCM16LE -> Fun-ASR.
//
// The Ogg file is used only to obtain realistic codec packets; the ASR
// request is sent as PCM decoded by libopus, without an FFmpeg decoder.
func TestAliyunLiveRawOpusPipeline(t *testing.T) {
	if os.Getenv("DASHSCOPE_ASR_MODEL") != ModelFunASRRealtime {
		t.Skip("native packet pipeline runs only against Fun-ASR")
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
	packets, err := extractRawOpusPackets(ogg)
	if err != nil {
		t.Fatal(err)
	}

	decoder, err := opus.NewDecoder(16000, 1)
	if err != nil {
		t.Fatalf("initialize native libopus: %v", err)
	}
	defer decoder.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	client, err := New(Config{Endpoint: endpoint, APIKey: key, Model: ModelFunASRRealtime})
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.OpenStream(ctx, asr.Config{
		Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
	})
	if err != nil {
		t.Fatalf("open live ASR: %v", err)
	}
	defer session.Close()

	type receiverResult struct {
		text string
		err  error
	}
	received := make(chan receiverResult, 1)
	go func() {
		var segments []string
		for {
			ev, recvErr := session.Recv(ctx)
			if errors.Is(recvErr, io.EOF) {
				received <- receiverResult{text: strings.Join(segments, " ")}
				return
			}
			if recvErr != nil {
				received <- receiverResult{err: recvErr}
				return
			}
			if ev.Type == asr.EventFinal {
				segments = append(segments, ev.Text)
			}
		}
	}()

	decodedBytes := 0
	for i, packet := range packets {
		pcm, err := decoder.Decode(packet)
		if err != nil {
			t.Fatalf("native Opus decode packet %d: %v", i, err)
		}
		if len(pcm) == 0 || len(pcm)%2 != 0 {
			t.Fatalf("invalid decoded PCM packet %d: %d bytes", i, len(pcm))
		}
		if err := session.Send(ctx, pcm); err != nil {
			t.Fatalf("send decoded PCM packet %d: %v", i, err)
		}
		decodedBytes += len(pcm)
		duration := time.Duration(len(pcm)/2) * time.Second / 16000
		select {
		case <-time.After(duration):
		case <-ctx.Done():
			t.Fatalf("streaming raw Opus timed out: %v", ctx.Err())
		}
	}
	if decodedBytes < 32000 {
		t.Fatalf("insufficient packet audio decoded: %d bytes", decodedBytes)
	}
	if err := session.CloseSend(ctx); err != nil {
		t.Fatalf("end live ASR: %v", err)
	}
	select {
	case result := <-received:
		if result.err != nil {
			t.Fatalf("live ASR failed: %v", result.err)
		}
		if strings.TrimSpace(result.text) == "" {
			t.Fatal("raw Opus packets -> libopus -> Fun-ASR produced no transcript")
		}
		t.Logf("Decoded %d raw Opus packets (%d PCM bytes); recognized %q",
			len(packets), decodedBytes, result.text)
	case <-ctx.Done():
		t.Fatalf("waiting for final text: %v", ctx.Err())
	}
}
