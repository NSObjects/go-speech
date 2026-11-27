//go:build integration

package volcengine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/tts"
	"github.com/gorilla/websocket"
)

// TestVolcengineLiveReuseProbe is a one-off protocol experiment, not a
// capability assertion. It deliberately bypasses the SDK's close-on-CLOSED
// behavior. Never pool a socket solely because this probe passes.
func TestVolcengineLiveReuseProbe(t *testing.T) {
	key, voice := os.Getenv("VOLCENGINE_API_KEY"), os.Getenv("VOLCENGINE_TTS_VOICE")
	if key == "" || voice == "" {
		t.Fatal("Seed-TTS credentials and voice required")
	}
	c, err := New(Config{APIKey: key, ModelID: os.Getenv("VOLCENGINE_TTS_MODEL_ID"), LanguageCode: "zh"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	_, init, err := c.prepareDuplex(tts.DuplexRequest{Voice: voice, Format: audio.Format{
		Encoding: audio.EncodingPCM16LE, SampleRate: 24000, Channels: 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	open := func(t *testing.T) *websocket.Conn {
		t.Helper()
		start, stop := context.WithTimeout(ctx, c.cfg.StartTimeout)
		defer stop()
		conn, resp, err := websocket.DefaultDialer.DialContext(start, c.endpoint(voice, "streaming-input", true), http.Header{"X-Api-Key": []string{key}})
		if err != nil {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			t.Fatal("baseline dial failed:", err)
		}
		conn.SetReadLimit(maxEventBytes)
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}
	// Fresh sockets for each idle duration; no heartbeat or keepalive
	// frames are sent. A successful baseline is required before measuring.
	for _, idle := range []time.Duration{15 * time.Second, 30 * time.Second} {
		t.Run(idle.String(), func(t *testing.T) {
			conn := open(t)
			if err := probeInit(t, conn, init); err != nil {
				t.Fatal("baseline init:", err)
			}
			for round := range 2 {
				if err := probeSend(conn, clientEvent{Event: "text", Content: "你好，这是空闲连接复用测试。"}); err != nil {
					t.Fatal("send:", err)
				}
				if err := probeSend(conn, clientEvent{Event: "close"}); err != nil {
					t.Fatal("close:", err)
				}
				data, reason, err := probeDrain(conn)
				if err != nil || reason != closedReason {
					t.Fatalf("round=%d CLOSED: %s, %v", round+1, reason, err)
				}
				verifyAdvancedPCM(t, data)
				if round == 1 {
					t.Logf("IDLE_REUSE_OBSERVED idle=%s audio_bytes=%d", idle, len(data))
					break
				}
				start := time.Now()
				timer := time.NewTimer(idle)
				select {
				case <-timer.C:
				case <-ctx.Done():
					timer.Stop()
					t.Fatal(ctx.Err())
				}
				if err := probeInit(t, conn, init); err != nil {
					t.Logf("IDLE_REUSE_NOT_ESTABLISHED idle=%s elapsed=%s error=%v", idle, time.Since(start), err)
					return
				}
			}
		})
	}
}

func probeInit(t *testing.T, conn *websocket.Conn, init clientEvent) error {
	t.Helper()
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return err
	}
	init.SessionID = hex.EncodeToString(id[:])
	start := time.Now()
	if err := probeSend(conn, init); err != nil {
		return err
	}
	if err := conn.SetReadDeadline(time.Now().Add(15 * time.Second)); err != nil {
		return err
	}
	e, err := probeRead(conn)
	if err != nil {
		return err
	}
	if e.Event != "ready" || e.SessionID != init.SessionID {
		return fmt.Errorf("expected matching ready, received event=%q", e.Event)
	}
	t.Logf("ready confirmed for new session, elapsed=%s", time.Since(start))
	return nil
}

func probeSend(conn *websocket.Conn, e clientEvent) error {
	if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	return conn.WriteJSON(e)
}

func probeRead(conn *websocket.Conn) (event, error) {
	typ, raw, err := conn.ReadMessage()
	if err != nil {
		return event{}, err
	}
	if typ != websocket.TextMessage {
		return event{}, fmt.Errorf("unexpected frame type %d", typ)
	}
	e, err := decodeEvent(raw)
	if err == nil && e.Event == "error" {
		return e, e.failure()
	}
	return e, err
}

func probeDrain(conn *websocket.Conn) ([]byte, string, error) {
	if err := conn.SetReadDeadline(time.Now().Add(20 * time.Second)); err != nil {
		return nil, "", err
	}
	var data []byte
	for {
		e, err := probeRead(conn)
		if err != nil {
			return data, "", err
		}
		switch e.Event {
		case "audio":
			if len(e.Audio) > maxBatchBytes-len(data) {
				return nil, "", fmt.Errorf("probe audio limit exceeded")
			}
			data = append(data, e.Audio...)
		case "alignment":
			// Not enabled in this probe; ignore optional alignment.
		case "final":
			if !e.IsFinal {
				return data, e.Reason, fmt.Errorf("invalid final")
			}
			return data, e.Reason, nil
		default:
			return data, "", fmt.Errorf("unexpected event %q", e.Event)
		}
	}
}
