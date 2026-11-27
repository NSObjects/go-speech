//go:build integration

package tencent

import (
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/internal/wireutil"
	"github.com/gorilla/websocket"
)

// Each binary message contains a standalone M4A file, not a slice of a
// larger MP4 container. Validate this documented transport with AAC-LC
// fixtures before exposing a production API.
func TestTencentLiveM4ASegmentsProbe(t *testing.T) {
	dir := os.Getenv("TENCENT_TEST_M4A_DIR")
	if dir == "" {
		t.Fatal("TENCENT_TEST_M4A_DIR is required")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var segments [][]byte
	total := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".m4a") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if len(raw) < 12 || string(raw[4:8]) != "ftyp" || len(raw) > 1<<20 {
			t.Fatal("invalid independent M4A fixture")
		}
		segments = append(segments, raw)
		total += len(raw)
	}
	if len(segments) < 3 || len(segments) > 60 {
		t.Fatalf("unexpected fixture segment count: %d", len(segments))
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
	target := c.signedURLWithOptions(time.Now(), 1+uint32(time.Now().UnixNano()%999999999), id, "", url.Values{"voice_format": {"14"}})
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
	for i, segment := range segments {
		// One complete independently encoded one-second M4A per message.
		if err := s.write(ctx, websocket.BinaryMessage, segment); err != nil {
			select {
			case result := <-done:
				t.Fatalf("M4A segment %d: %v; receive: %v", i, err, result.err)
			default:
				t.Fatalf("M4A segment %d: %v", i, err)
			}
		}
		select {
		case result := <-done:
			t.Fatalf("premature M4A completion after segment %d: %v", i, result.err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Second):
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
			t.Fatal("M4A returned no confirmed text")
		}
		t.Logf("real Tencent M4A probe: %d bytes, %d independent segments, %d confirmed characters", total, len(segments), len([]rune(result.text)))
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for i := 0; i < 2; i++ {
		if _, err := s.Recv(ctx); !errors.Is(err, io.EOF) {
			t.Fatalf("terminal EOF %d: %v", i, err)
		}
	}
}
