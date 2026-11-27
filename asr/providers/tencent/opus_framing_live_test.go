//go:build integration

package tencent

import (
	"bytes"
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

// The independent fixture uses the verified native frame layout. Exercise
// both public entry points and rebuild each frame using the SDK helper.
func TestTencentLiveNativeOpus(t *testing.T) {
	raw, err := os.ReadFile(os.Getenv("TENCENT_TEST_OPUS_LE_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	frames, err := nativeOpusFrames(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) < 25 {
		t.Fatal("insufficient fixture audio")
	}
	var rebuilt []byte
	for _, frame := range frames {
		packet, err := nativeOpusPacket(frame)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := FrameOpusPacket(packet)
		if err != nil || !bytes.Equal(encoded, frame) {
			t.Fatalf("SDK frame differs from independent fixture: %v", err)
		}
		rebuilt = append(rebuilt, encoded...)
	}
	c, err := New(Config{
		AppID: os.Getenv("TENCENT_APP_ID"), SecretID: os.Getenv("TENCENT_SECRET_ID"),
		SecretKey: os.Getenv("TENCENT_SECRET_KEY"), EngineModel: "16k_zh", NeedVAD: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	f := audio.Format{Encoding: EncodingOpusFrames, SampleRate: 16000, Channels: 1}
	t.Run("recognize", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		res, err := c.Recognize(ctx, audio.Buffer{Data: rebuilt, Format: f}, asr.Config{})
		if err != nil || strings.TrimSpace(res.Text) == "" {
			t.Fatalf("native Opus Recognize: %v", err)
		}
		t.Logf("native Opus Recognize: %d frames, %d characters", len(frames), len([]rune(res.Text)))
	})
	t.Run("stream", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		s, err := c.OpenStream(ctx, asr.Config{Format: f})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		type outcome struct {
			text string
			err  error
		}
		done := make(chan outcome, 1)
		go func() {
			var parts []string
			for {
				ev, err := s.Recv(ctx)
				if errors.Is(err, io.EOF) {
					done <- outcome{text: strings.Join(parts, "")}
					return
				}
				if err != nil {
					done <- outcome{err: err}
					return
				}
				if ev.Type == asr.EventFinal {
					parts = append(parts, ev.Text)
				}
			}
		}()
		for i, frame := range frames {
			if err := s.Send(ctx, frame); err != nil {
				t.Fatalf("frame %d: %v", i, err)
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(nativeOpusDuration):
			}
		}
		if err := s.CloseSend(ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case res := <-done:
			if res.err != nil || strings.TrimSpace(res.text) == "" {
				t.Fatalf("native Opus stream: %v", res.err)
			}
			t.Logf("native Opus stream: %d frames, %d characters", len(frames), len([]rune(res.text)))
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		for i := 0; i < 2; i++ {
			if _, err := s.Recv(ctx); !errors.Is(err, io.EOF) {
				t.Fatalf("terminal EOF %d: %v", i, err)
			}
		}
	})
}
