//go:build integration

package volcengine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strings"
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
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
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
	t.Run("closed_then_new_init", func(t *testing.T) {
		conn := open(t)
		if err := probeInit(t, conn, init); err != nil {
			t.Fatal("baseline init:", err)
		}
		if err := probeSend(conn, clientEvent{Event: "text", Content: "你好，欢迎使用语音合成。"}); err != nil {
			t.Fatal(err)
		}
		if err := probeSend(conn, clientEvent{Event: "close"}); err != nil {
			t.Fatal(err)
		}
		data, reason, err := probeDrain(conn)
		if err != nil || reason != closedReason {
			t.Fatalf("baseline CLOSED: %s, %v", reason, err)
		}
		verifyAdvancedPCM(t, data)
		t.Logf("baseline CLOSED, bytes=%d; attempting second init on same socket", len(data))
		if err := probeInit(t, conn, init); err != nil {
			t.Logf("REUSE_NOT_ESTABLISHED phase=second_init error=%v", err)
			return
		}
		if err := probeSend(conn, clientEvent{Event: "text", Content: "这是第二个独立的语音任务。"}); err != nil {
			t.Logf("REUSE_NOT_ESTABLISHED phase=second_text error=%v", err)
			return
		}
		if err := probeSend(conn, clientEvent{Event: "close"}); err != nil {
			t.Logf("REUSE_NOT_ESTABLISHED phase=second_close error=%v", err)
			return
		}
		data, reason, err = probeDrain(conn)
		if err != nil || reason != closedReason {
			t.Logf("REUSE_NOT_ESTABLISHED phase=second_final reason=%q error=%v", reason, err)
			return
		}
		verifyAdvancedPCM(t, data)
		t.Logf("REUSE_OBSERVED two distinct ready sessions and CLOSED finals on one socket; second bytes=%d", len(data))
	})

	t.Run("interrupt", func(t *testing.T) {
		conn := open(t)
		if err := probeInit(t, conn, init); err != nil {
			t.Fatal("baseline init:", err)
		}
		if err := probeSend(conn, clientEvent{Event: "text", Content: strings.Repeat("这是一段用于验证语音打断行为的测试文本。", 12)}); err != nil {
			t.Fatal(err)
		}
		if err := probeSend(conn, clientEvent{Event: "flush"}); err != nil {
			t.Fatal(err)
		}
		if err := conn.SetReadDeadline(time.Now().Add(20 * time.Second)); err != nil {
			t.Fatal(err)
		}
		for {
			e, err := probeRead(conn)
			if err != nil {
				t.Fatal("baseline audio:", err)
			}
			if e.Event == "audio" && len(e.Audio) != 0 {
				break
			}
			if e.Event == "final" {
				t.Fatal("baseline ended before audio")
			}
		}
		start := time.Now()
		if err := probeSend(conn, clientEvent{Event: "interrupt"}); err != nil {
			t.Logf("INTERRUPT_NOT_ESTABLISHED phase=send error=%v", err)
			return
		}
		data, reason, err := probeDrain(conn)
		t.Logf("INTERRUPT_OBSERVED elapsed=%s post_interrupt_audio_bytes=%d final_reason=%q error=%v", time.Since(start), len(data), reason, err)
		// FLUSHED alone does not acknowledge cancellation; it can also
		// mean ordinary completion. Never claim safe reuse from it.
		if err == nil && reason == closedReason {
			if err := probeInit(t, conn, init); err != nil {
				t.Logf("INTERRUPT_REINIT_NOT_ESTABLISHED error=%v", err)
			} else {
				t.Log("INTERRUPT_REINIT_READY observed; audio isolation still unverified")
			}
		}
	})
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
