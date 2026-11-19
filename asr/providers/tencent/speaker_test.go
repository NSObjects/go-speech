package tencent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/gorilla/websocket"
)

// Both -1 (speaker pending) and speaker zero are valid Tencent values.
// Missing speaker_id must remain nil rather than looking like speaker zero.
func TestTencentV2SpeakerMetadataPreservesMissingPendingAndZero(t *testing.T) {
	client := testClient(t, func(ws *websocket.Conn, req *http.Request) {
		if got := req.URL.Query().Get("engine_model_type"); got != "16k_zh_en_speaker_2.0" {
			t.Errorf("wrong speaker engine %q", got)
		}
		if !verifySignature(req, "secret-key") {
			t.Error("invalid V2 speaker engine HMAC signature")
		}
		id := req.URL.Query().Get("voice_id")
		if err := sendResponse(ws, id, 0, 0, nil); err != nil {
			return
		}
		if typ, _, err := ws.ReadMessage(); err != nil || typ != websocket.BinaryMessage {
			t.Errorf("expected PCM upload before speaker events, type=%d err=%v", typ, err)
			return
		}
		send := func(final int, sentences []map[string]any) {
			data, err := json.Marshal(map[string]any{
				"code": 0, "voice_id": id, "final": final,
				"sentences": map[string]any{"sentence_list": sentences},
			})
			if err != nil {
				t.Error(err)
				return
			}
			if err := ws.WriteMessage(websocket.TextMessage, data); err != nil {
				t.Error(err)
			}
		}
		send(0, []map[string]any{
			{"sentence_id": 0, "sentence_type": 0, "sentence": "你好", "speaker_id": -1},
			{"sentence_id": 1, "sentence_type": 0, "sentence": "世界", "speaker_id": 0},
			{"sentence_id": 2, "sentence_type": 0, "sentence": "未标注"},
		})
		if typ, _, err := ws.ReadMessage(); err != nil || typ != websocket.TextMessage {
			t.Errorf("expected end frame, type=%d err=%v", typ, err)
			return
		}
		send(1, []map[string]any{
			{"sentence_id": 0, "sentence_type": 1, "sentence": "你好", "speaker_id": 0},
			{"sentence_id": 0, "sentence_type": 1, "sentence": "你好", "speaker_id": 0},
			{"sentence_id": 1, "sentence_type": 1, "sentence": "世界", "speaker_id": 1},
			{"sentence_id": 2, "sentence_type": 1, "sentence": "未标注"},
			{"sentence_id": 3, "sentence_type": 1, "sentence": "再见", "speaker_id": 9},
		})
	})
	client.cfg.EngineModel = "16k_zh_en_speaker_2.0"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s, err := client.OpenStream(ctx, pcmConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Send(ctx, []byte{1, 0}); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseSend(ctx); err != nil {
		t.Fatal(err)
	}

	pending := -1
	zero := 0
	one := 1
	nine := 9
	for i, want := range []struct {
		kind    asr.EventType
		text    string
		speaker *int
	}{
		{asr.EventPartial, "你好", &pending},
		{asr.EventPartial, "世界", &zero},
		{asr.EventPartial, "未标注", nil},
		{asr.EventFinal, "你好", &zero},
		{asr.EventFinal, "世界", &one},
		{asr.EventFinal, "未标注", nil},
		{asr.EventFinal, "再见", &nine},
	} {
		got, err := s.Recv(ctx)
		if err != nil {
			t.Fatalf("speaker event %d: %v", i, err)
		}
		if got.Type != want.kind || got.Text != want.text {
			t.Fatalf("speaker event %d = %+v, want kind=%s text=%q", i, got, want.kind, want.text)
		}
		if (got.SpeakerID == nil) != (want.speaker == nil) {
			t.Fatalf("speaker event %d speaker presence: got %+v, want %v", i, got.SpeakerID, want.speaker)
		}
		if want.speaker != nil && *got.SpeakerID != *want.speaker {
			t.Fatalf("speaker event %d speaker = %d, want %d", i, *got.SpeakerID, *want.speaker)
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := s.Recv(context.Background()); !errors.Is(err, io.EOF) {
			t.Fatalf("speaker stream final EOF %d = %v", i, err)
		}
	}
}

// JSON's absent speaker_id must not be decoded as speaker zero.
// Explicit -1, 0 and 9 must survive the wire parser unchanged.
func TestTencentV2SpeakerIDJSONPresence(t *testing.T) {
	var sentences []sentence
	if err := json.Unmarshal([]byte(`[
		{"sentence_id":0,"sentence_type":0,"speaker_id":-1},
		{"sentence_id":1,"sentence_type":0,"speaker_id":0},
		{"sentence_id":2,"sentence_type":1,"speaker_id":9},
		{"sentence_id":3,"sentence_type":1}
	]`), &sentences); err != nil {
		t.Fatal(err)
	}
	for i, want := range []int{-1, 0, 9} {
		if sentences[i].SpeakerID == nil || *sentences[i].SpeakerID != want {
			t.Fatalf("speaker %d: got %v, want %d", i, sentences[i].SpeakerID, want)
		}
	}
	if sentences[3].SpeakerID != nil {
		t.Fatalf("missing speaker_id decoded as %+v", sentences[3].SpeakerID)
	}
}
