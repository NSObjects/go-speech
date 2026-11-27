//go:build integration

package tencent

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/internal/wireutil"
	"github.com/gorilla/websocket"
)

// The ASR specification defines "opus" + two-byte length + one encoded
// 640-sample frame but leaves byte order unspecified. This probe tests a
// single little-endian hypothesis using real libopus packets at 16 kHz.
// It does not advertise support or change the production client's format.
func TestTencentLiveNativeOpusFramingProbe(t *testing.T) {
	raw, err := os.ReadFile(os.Getenv("TENCENT_TEST_OPUS_LE_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	var frames [][]byte
	for len(raw) != 0 {
		if len(raw) < 6 || string(raw[:4]) != "opus" {
			t.Fatal("invalid probe frame header")
		}
		n := int(binary.LittleEndian.Uint16(raw[4:6]))
		if n == 0 || n > 1275 || len(raw) < 6+n {
			t.Fatal("invalid probe frame length")
		}
		frames = append(frames, raw[:6+n])
		raw = raw[6+n:]
	}
	if len(frames) < 25 || len(frames) > 1500 {
		t.Fatalf("unexpected fixture frame count: %d", len(frames))
	}
	c, err := New(Config{
		AppID: os.Getenv("TENCENT_APP_ID"), SecretID: os.Getenv("TENCENT_SECRET_ID"),
		SecretKey: os.Getenv("TENCENT_SECRET_KEY"), EngineModel: "16k_zh", NeedVAD: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	id, err := wireutil.UUIDv4()
	if err != nil {
		t.Fatal(err)
	}
	// Test-only signed handshake. Do not expose the signed URL in logs.
	target := c.signedURLWithOptions(time.Now(), 1+uint32(time.Now().UnixNano()%999999999), id, "", url.Values{"voice_format": {"10"}})
	d := *websocket.DefaultDialer
	d.HandshakeTimeout = c.cfg.StartTimeout
	conn, resp, err := d.DialContext(ctx, target, nil)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		t.Fatal(connectError(err, resp))
	}
	conn.SetReadLimit(1 << 20)
	s := newStream(conn, ctx, id)
	defer s.Close()
	if err := s.awaitHandshake(ctx, c.cfg.StartTimeout); err != nil {
		t.Fatal(err)
	}
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
		// Direct test-only binary send bypasses the current PCM/WAV format
		// restrictions. One complete 640-sample packet is 40ms at 16 kHz.
		if err := s.write(ctx, websocket.BinaryMessage, frame); err != nil {
			select {
			case result := <-done:
				t.Fatalf("Opus frame %d: %v; receive: %v", i, err, result.err)
			default:
				t.Fatalf("Opus frame %d: %v", i, err)
			}
		}
		select {
		case result := <-done:
			t.Fatalf("premature Opus completion after frame %d: %v", i, result.err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(40 * time.Millisecond):
		}
	}
	if err := s.write(ctx, websocket.TextMessage, []byte(`{"type":"end"}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatal(result.err)
		}
		if strings.TrimSpace(result.text) == "" {
			t.Fatal("native Opus returned no confirmed text")
		}
		t.Logf("real native Opus framing LE: %d 40ms packets, %d confirmed characters", len(frames), len([]rune(result.text)))
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for i := 0; i < 2; i++ {
		if _, err := s.Recv(ctx); !errors.Is(err, io.EOF) {
			t.Fatalf("terminal EOF %d: %v", i, err)
		}
	}
}
