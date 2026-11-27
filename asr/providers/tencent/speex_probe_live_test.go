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

type speexProbePage struct {
	raw     []byte
	samples uint64
}

// splitSpeexProbePages parses the independent fixture only. Do not expose
// an Ogg parser or advertise Speex support until the cloud accepts it.
func splitSpeexProbePages(raw []byte) ([]speexProbePage, error) {
	var pages []speexProbePage
	var serial, sequence uint32
	var last uint64
	for len(raw) > 0 {
		if len(raw) < 27 || string(raw[:4]) != "OggS" || raw[4] != 0 {
			return nil, errors.New("invalid Ogg page")
		}
		count := int(raw[26])
		if len(raw) < 27+count {
			return nil, errors.New("truncated lacing table")
		}
		size := 27 + count
		for _, n := range raw[27:size] {
			size += int(n)
		}
		if size > len(raw) {
			return nil, errors.New("truncated page")
		}
		s := binary.LittleEndian.Uint32(raw[14:18])
		q := binary.LittleEndian.Uint32(raw[18:22])
		if len(pages) == 0 {
			if count != 1 || raw[27] != 80 || size < 108 || string(raw[28:36]) != "Speex   " ||
				binary.LittleEndian.Uint32(raw[64:68]) != 16000 ||
				binary.LittleEndian.Uint32(raw[76:80]) != 1 {
				return nil, errors.New("expected mono 16k Speex header")
			}
			serial, sequence = s, q
		} else {
			if s != serial || q != sequence+1 {
				return nil, errors.New("fixture contains discontinuous Ogg pages")
			}
			sequence = q
		}
		granule := binary.LittleEndian.Uint64(raw[6:14])
		var samples uint64
		if granule != ^uint64(0) {
			if granule < last || granule > 16000*60 {
				return nil, errors.New("invalid fixture granule")
			}
			samples = granule - last
			last = granule
		}
		pages = append(pages, speexProbePage{raw: raw[:size], samples: samples})
		raw = raw[size:]
	}
	if len(pages) < 3 || last < 16000 || last > 16000*60 || pages[len(pages)-1].raw[5]&4 == 0 {
		return nil, errors.New("incomplete fixture")
	}
	return pages, nil
}

// The public ASR protocol specifies voice_format=4 (Speex/SP), but does not
// document the container. Test standard Ogg Speex as one explicit hypothesis.
// No production client changes, fallback, retries, or advertised capability.
func TestTencentLiveOggSpeexProbe(t *testing.T) {
	raw, err := os.ReadFile(os.Getenv("TENCENT_TEST_SPEEX_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	pages, err := splitSpeexProbePages(raw)
	if err != nil {
		t.Fatal(err)
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
	target := c.signedURLWithOptions(time.Now(), 1+uint32(time.Now().UnixNano()%999999999), id, "", url.Values{"voice_format": {"4"}})
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
	for i, page := range pages {
		// Send exact Ogg pages at their audio cadence, including the original
		// headers. Bypass PCM validation only inside this protocol diagnostic.
		if err := s.write(ctx, websocket.BinaryMessage, page.raw); err != nil {
			select {
			case result := <-done:
				t.Fatalf("Speex page %d: %v; receive: %v", i, err, result.err)
			default:
				t.Fatalf("Speex page %d: %v", i, err)
			}
		}
		select {
		case result := <-done:
			t.Fatalf("premature Speex completion after page %d: %v", i, result.err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Duration(page.samples) * time.Second / 16000):
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
			t.Fatal("Ogg Speex returned no confirmed text")
		}
		t.Logf("real Tencent Ogg Speex probe: %d bytes, %d Ogg pages, %d confirmed characters", len(raw), len(pages), len([]rune(result.text)))
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for i := 0; i < 2; i++ {
		if _, err := s.Recv(ctx); !errors.Is(err, io.EOF) {
			t.Fatalf("terminal EOF %d: %v", i, err)
		}
	}
}
