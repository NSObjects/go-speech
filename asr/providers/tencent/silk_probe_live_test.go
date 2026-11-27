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

type silkProbeSegment struct {
	data    []byte
	packets int
}

// Parse only the independently encoded fixture: standard SILK V3 header,
// little-endian signed 16-bit packet length and final -1 marker. Do not
// interpret this as a verified Tencent-specific format yet.
func splitSILKProbe(data []byte) ([]silkProbeSegment, error) {
	const header = "#!SILK_V3"
	if len(data) < len(header)+4 || len(data) > 1<<20 || string(data[:len(header)]) != header {
		return nil, errors.New("invalid SILK V3 fixture header")
	}
	var out []silkProbeSegment
	start, pos, packets := 0, len(header), 0
	for pos < len(data) {
		if len(data)-pos < 2 {
			return nil, errors.New("truncated SILK length")
		}
		n := int(int16(binary.LittleEndian.Uint16(data[pos : pos+2])))
		if n == -1 {
			if pos+2 != len(data) || packets < 50 || len(out) == 0 {
				return nil, errors.New("invalid SILK end marker")
			}
			if pos > start {
				out = append(out, silkProbeSegment{data: data[start:], packets: packets % 10})
			} else {
				// Preserve the terminal marker with the final packet group.
				out[len(out)-1].data = data[len(data)-2-len(out[len(out)-1].data):]
			}
			return out, nil
		}
		if n <= 0 || n > 1250 || n > len(data)-pos-2 || packets >= 3000 {
			return nil, errors.New("invalid SILK packet length or duration")
		}
		pos += 2 + n
		packets++
		if packets%10 == 0 {
			out = append(out, silkProbeSegment{data: data[start:pos], packets: 10})
			start = pos
		}
	}
	return nil, errors.New("missing SILK end marker")
}

// A standard SILK V3 container is one explicit candidate for voice_format=6.
// Reference encoder built outside the credential-bearing test job.
func TestTencentLiveSILKV3Probe(t *testing.T) {
	raw, err := os.ReadFile(os.Getenv("TENCENT_TEST_SILK_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	segments, err := splitSILKProbe(raw)
	if err != nil {
		t.Fatal(err)
	}
	total := len(raw)

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
	target := c.signedURLWithOptions(time.Now(), 1+uint32(time.Now().UnixNano()%999999999), id, "", url.Values{"voice_format": {"6"}})
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
		// Send original container bytes in complete, paced packet groups.
		if err := s.write(ctx, websocket.BinaryMessage, segment.data); err != nil {
			select {
			case result := <-done:
				t.Fatalf("SILK segment %d: %v; receive: %v", i, err, result.err)
			default:
				t.Fatalf("SILK segment %d: %v", i, err)
			}
		}
		select {
		case result := <-done:
			t.Fatalf("premature SILK completion after segment %d: %v", i, result.err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Duration(segment.packets) * 20 * time.Millisecond):
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
			t.Fatal("SILK returned no confirmed text")
		}
		t.Logf("real Tencent SILK probe: %d bytes, %d packet groups, %d confirmed characters", total, len(segments), len([]rune(result.text)))
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for i := 0; i < 2; i++ {
		if _, err := s.Recv(ctx); !errors.Is(err, io.EOF) {
			t.Fatalf("terminal EOF %d: %v", i, err)
		}
	}
}
