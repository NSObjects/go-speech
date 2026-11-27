//go:build integration

package volcengine

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/internal/wireutil"
	"github.com/gorilla/websocket"
)

// Probe the physical transport, not Stream.Close, which always closes the
// socket. A successful first recognition is mandatory. A second init on the
// same connection is experimental and must not be interpreted as supported
// merely because a write succeeds.
func TestVolcengineLiveReuseProbe(t *testing.T) {
	raw, err := os.ReadFile(os.Getenv("VOLCENGINE_TEST_PCM_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(Config{APIKey: os.Getenv("VOLCENGINE_API_KEY"), ResourceID: os.Getenv("VOLCENGINE_RESOURCE_ID"), Endpoint: os.Getenv("VOLCENGINE_WS_ENDPOINT")})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	req := asr.Config{Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1}}
	payload, _, _, err := c.prepare(req)
	if err != nil {
		t.Fatal(err)
	}
	init, err := encodeFrame(messageFullRequest, 0, 1, payload)
	if err != nil {
		t.Fatal(err)
	}
	id, err := wireutil.UUIDv4()
	if err != nil {
		t.Fatal(err)
	}
	h := http.Header{}
	h.Set("X-Api-Key", c.cfg.APIKey)
	h.Set("X-Api-Resource-Id", c.cfg.ResourceID)
	h.Set("X-Api-Request-Id", id)
	h.Set("X-Api-Connect-Id", id)
	h.Set("X-Api-Sequence", "-1")
	dialer := *websocket.DefaultDialer
	conn, resp, err := dialer.DialContext(ctx, c.cfg.Endpoint, h)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		t.Fatal("baseline dial failed:", connectError(err, resp))
	}
	defer conn.Close()
	conn.SetReadLimit(maxWirePayload + 128)
	send := func(frame []byte) error {
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		return conn.WriteMessage(websocket.BinaryMessage, frame)
	}
	read := func() ([]recognition, bool, error) {
		typ, data, err := conn.ReadMessage()
		if err != nil {
			return nil, false, err
		}
		if typ != websocket.BinaryMessage {
			return nil, false, fmt.Errorf("unexpected response frame type %d", typ)
		}
		m, err := parseFrame(data)
		if err != nil {
			return nil, false, err
		}
		r, last, _, err := m.result()
		return r, last, err
	}
	round := func() error {
		_ = conn.SetReadDeadline(time.Now().Add(35 * time.Second))
		if err := send(init); err != nil {
			return fmt.Errorf("init write: %w", err)
		}
		_, last, err := read()
		if err != nil {
			return fmt.Errorf("init response: %w", err)
		}
		if last {
			return fmt.Errorf("unexpected terminal init response")
		}
		done := make(chan error, 1)
		go func() {
			hasText := false
			for {
				results, last, err := read()
				if err != nil {
					done <- err
					return
				}
				for _, r := range results {
					hasText = hasText || strings.TrimSpace(r.Text) != ""
					for _, u := range r.Utterances {
						hasText = hasText || strings.TrimSpace(u.Text) != ""
					}
				}
				if last {
					if !hasText {
						done <- fmt.Errorf("terminal response without recognized text")
					} else {
						done <- nil
					}
					return
				}
			}
		}()
		const chunk = 6400
		for offset := 0; offset < len(raw); offset += chunk {
			end := min(offset+chunk, len(raw))
			frame, err := encodeFrame(messageAudioRequest, 0, 0, raw[offset:end])
			if err != nil {
				return err
			}
			if err := send(frame); err != nil {
				return fmt.Errorf("audio write: %w", err)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case err := <-done:
				if err == nil {
					err = fmt.Errorf("final before client finish")
				}
				return err
			case <-time.After(200 * time.Millisecond):
			}
		}
		end, err := encodeFrame(messageAudioRequest, flagLast, 0, nil)
		if err != nil {
			return err
		}
		if err := send(end); err != nil {
			return fmt.Errorf("finish write: %w", err)
		}
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := round(); err != nil {
		t.Fatal("baseline failed:", err)
	}
	t.Log("BASELINE_FINAL_OBSERVED on physical socket")
	if err := round(); err != nil {
		t.Logf("ASR_REUSE_NOT_ESTABLISHED: %v", err)
		return
	}
	t.Log("ASR_SECOND_FINAL_OBSERVED; handshake request identity still unchanged, requires further validation")
}
