package speech_test

import (
	"context"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	speech "github.com/NSObjects/go-speech"
	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/NSObjects/go-speech/tts"
	"github.com/gorilla/websocket"
)

func TestPublicPoolErrors(t *testing.T) {
	ctx := context.Background()
	var dials atomic.Int32
	dialer := &websocket.Dialer{NetDialContext: func(context.Context, string, string) (net.Conn, error) {
		dials.Add(1)
		return nil, errors.New("unexpected dial")
	}}
	cfg := &speech.PoolConfig{MaxOpen: 1, MaxIdle: 1}
	format := audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1}
	recognizer, err := speech.NewASR(speech.AliyunASR{APIKey: "test", Model: "fun-asr-realtime", Pool: cfg, Dialer: dialer})
	if err != nil {
		t.Fatal(err)
	}
	if err := recognizer.(io.Closer).Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := recognizer.OpenStream(ctx, asr.Config{Format: format}); !speecherr.IsCode(err, speecherr.CodeUnavailable) {
		t.Fatalf("closed ASR pool: %v", err)
	}
	for _, tc := range []struct {
		name string
		voice string
		config func(*speech.PoolConfig) speech.TTSConfig
	}{
		{"aliyun", "longxiaochun", func(pool *speech.PoolConfig) speech.TTSConfig {
			return speech.AliyunTTS{APIKey: "test", Pool: pool, Dialer: dialer}
		}},
		{"volcengine", "test-voice", func(pool *speech.PoolConfig) speech.TTSConfig {
			return speech.VolcengineTTS{APIKey: "test", Pool: pool, Dialer: dialer}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			unpooled, err := speech.NewDuplexTTS(tc.config(nil))
			if err != nil {
				t.Fatal(err)
			}
			defer unpooled.(io.Closer).Close()
			if err := unpooled.(tts.Prewarmer).Warmup(ctx, tc.voice, 1); !speecherr.IsCode(err, speecherr.CodeUnsupported) {
				t.Fatalf("disabled warmup: %v", err)
			}
			client, err := speech.NewDuplexTTS(tc.config(cfg))
			if err != nil {
				t.Fatal(err)
			}
			defer client.(io.Closer).Close()
			warmer := client.(tts.Prewarmer)
			for _, count := range []int{-1, 2} {
				if err := warmer.Warmup(ctx, tc.voice, count); !speecherr.IsCode(err, speecherr.CodeInvalidArgument) {
					t.Fatalf("warmup count %d: %v", count, err)
				}
			}
			if err := client.(io.Closer).Close(); err != nil {
				t.Fatal(err)
			}
			if err := warmer.Warmup(ctx, tc.voice, 1); !speecherr.IsCode(err, speecherr.CodeUnavailable) {
				t.Fatalf("closed warmup: %v", err)
			}
			_, err = client.OpenDuplex(ctx, tts.DuplexRequest{Voice: tc.voice, Format: format})
			var classified *speecherr.Error
			if !errors.As(err, &classified) || classified.Code != speecherr.CodeUnavailable || classified.Retryable {
				t.Fatalf("closed duplex pool: %v", err)
			}
		})
	}
	if dials.Load() != 0 {
		t.Fatalf("invalid/closed pool attempted %d network dials", dials.Load())
	}
}

func TestPublicPoolDeadline(t *testing.T) {
	var dials atomic.Int32
	dialer := &websocket.Dialer{NetDialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		dials.Add(1)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	cfg := &speech.PoolConfig{MaxOpen: 1, AcquireTimeout: 25 * time.Millisecond}
	format := audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1}
	c, err := speech.NewASR(speech.AliyunASR{APIKey: "test", Model: "fun-asr-realtime", Pool: cfg, Dialer: dialer})
	if err != nil {
		t.Fatal(err)
	}
	defer c.(io.Closer).Close()
	_, err = c.OpenStream(context.Background(), asr.Config{Format: format})
	if !speecherr.IsCode(err, speecherr.CodeTimeout) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("pool-local deadline: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	_, err = c.OpenStream(ctx, asr.Config{Format: format})
	if err != context.DeadlineExceeded {
		t.Fatalf("caller deadline was changed: %v", err)
	}
	if dials.Load() != 1 {
		t.Fatalf("unexpected extra dials: %d", dials.Load())
	}
}
