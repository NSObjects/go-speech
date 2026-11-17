//go:build integration && libopus && cgo

package aliyun

import (
	"bytes"
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
	"github.com/NSObjects/go-speech/audio/codec/opus"
)

// extractRawOpusPackets reads an Ogg Opus *test fixture* and extracts codec
// packets, skipping OpusHead and OpusTags. Ogg lacing may split a packet
// across pages, so the remainder is retained until the terminating segment.
// This is a test helper, not a production Ogg demuxer or RTP parser.
func extractRawOpusPackets(ogg []byte) ([][]byte, error) {
	var packets [][]byte
	var pending []byte
	headerPackets := 0
	for offset := 0; offset < len(ogg); {
		if len(ogg)-offset < 27 || !bytes.Equal(ogg[offset:offset+4], []byte("OggS")) || ogg[offset+4] != 0 {
			return nil, fmt.Errorf("invalid Ogg page at offset %d", offset)
		}
		count := int(ogg[offset+26])
		if len(ogg)-offset < 27+count {
			return nil, fmt.Errorf("truncated Ogg lacing table at %d", offset)
		}
		table := ogg[offset+27 : offset+27+count]
		payloadLength := 0
		for _, n := range table {
			payloadLength += int(n)
		}
		payloadAt := offset + 27 + count
		if len(ogg)-payloadAt < payloadLength {
			return nil, fmt.Errorf("truncated Ogg payload at %d", offset)
		}
		payload := ogg[payloadAt : payloadAt+payloadLength]
		at := 0
		for _, segment := range table {
			end := at + int(segment)
			pending = append(pending, payload[at:end]...)
			at = end
			if segment == 255 {
				continue
			}
			switch headerPackets {
			case 0:
				if !bytes.HasPrefix(pending, []byte("OpusHead")) {
					return nil, errors.New("Ogg missing OpusHead packet")
				}
			case 1:
				if !bytes.HasPrefix(pending, []byte("OpusTags")) {
					return nil, errors.New("Ogg missing OpusTags packet")
				}
			default:
				if len(pending) > 0 {
					packets = append(packets, pending)
				}
			}
			headerPackets++
			pending = nil
		}
		offset = payloadAt + payloadLength
	}
	if len(pending) != 0 {
		return nil, errors.New("incomplete final Ogg packet")
	}
	if len(packets) == 0 {
		return nil, errors.New("Ogg fixture contains no raw Opus audio packets")
	}
	return packets, nil
}

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
