package aliyun

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/gorilla/websocket"
)

func TestDynamicContextAndUsageWire(t *testing.T) {
	for _, model := range []string{ModelQwenAudio31Message, ModelQwenAudio31Streaming} {
		t.Run(model, func(t *testing.T) {
			dynamic := model == ModelQwenAudio31Message
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upgrader := websocket.Upgrader{}
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
				var run taskMessage
				if err := conn.ReadJSON(&run); err != nil {
					t.Error(err)
					return
				}
				send := func(event, payload string) bool {
					err := conn.WriteJSON(map[string]any{
						"header": map[string]any{"event": event, "task_id": run.Header.TaskID},
						"payload": json.RawMessage(payload),
					})
					if err != nil {
						t.Error(err)
					}
					return err == nil
				}
				if !send("task-started", "{}") {
					return
				}
				typ, _, err := conn.ReadMessage()
				if err != nil || typ != websocket.BinaryMessage {
					t.Errorf("initial audio: %d, %v", typ, err)
					return
				}
				if !send("result-generated", `{"output":{"sentence":{"sentence_end":true,"text":"第一句"}},"usage":{"duration":1,"input_tokens":10,"output_tokens":2,"total_tokens":12}}`) {
					return
				}
				if dynamic {
					var cmd taskMessage
					if err := conn.ReadJSON(&cmd); err != nil {
						t.Error(err)
						return
					}
					b, err := json.Marshal(cmd.Payload.Input["context"])
					if err != nil || cmd.Header.Action != "continue-task" || cmd.Header.TaskID != run.Header.TaskID || cmd.Header.Streaming != "duplex" ||
						string(b) != `[{"content":[{"text":"Eino","type":"input_text"}],"role":"user"},{"content":[{"text":"明白","type":"text"}],"role":"assistant"}]` {
						t.Errorf("context command = %+v, %s, %v", cmd, b, err)
						return
					}
				}
				typ, _, err = conn.ReadMessage()
				if err != nil || typ != websocket.BinaryMessage {
					t.Errorf("next audio: %d, %v", typ, err)
					return
				}
				if !send("result-generated", `{"output":{"sentence":{"heartbeat":true}},"usage":{"duration":2,"input_tokens":20,"output_tokens":2,"total_tokens":22}}`) ||
					!send("result-generated", `{"output":{"sentence":{"sentence_end":true,"text":"第二句"}}}`) {
					return
				}
				var finish taskMessage
				if err := conn.ReadJSON(&finish); err != nil || finish.Header.Action != "finish-task" {
					t.Errorf("finish: %+v, %v", finish, err)
					return
				}
				send("task-finished", `{"output":{},"usage":{"duration":2,"input_tokens":20,"output_tokens":4,"total_tokens":24}}`)
			}))
			defer server.Close()
			c, err := New(Config{APIKey: "test", Model: model, Endpoint: "ws" + strings.TrimPrefix(server.URL, "http") + "/api-ws/v1/inference"})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			s, err := c.OpenStream(ctx, asr.Config{Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1}})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			updater := s.(ContextUpdater)
			metadata := s.(asr.SessionMetadataReceiver)
			if metadata.SessionMetadata() != nil {
				t.Fatal("usage fabricated before response")
			}
			if err := s.Send(ctx, []byte{0, 0}); err != nil {
				t.Fatal(err)
			}
			if e, err := s.Recv(ctx); err != nil || e.Text != "第一句" || e.Type != asr.EventFinal {
				t.Fatalf("first sentence = %+v, %v", e, err)
			}
			u := metadata.SessionMetadata().(SessionDetails).Usage
			if u.Final || u.TotalTokens == nil || *u.TotalTokens != 12 {
				t.Fatalf("initial usage = %+v", u)
			}
			*u.TotalTokens = 999
			if *metadata.SessionMetadata().(SessionDetails).Usage.TotalTokens != 12 {
				t.Fatal("usage snapshot is not independent")
			}
			messages := []ContextMessage{{Role: "user", Text: "Eino"}, {Role: "assistant", Text: "明白"}}
			if dynamic {
				if err := updater.UpdateContext(ctx, nil); !speecherr.IsCode(err, speecherr.CodeInvalidArgument) {
					t.Fatalf("empty context: %v", err)
				}
				if err := updater.UpdateContext(ctx, []ContextMessage{{Role: "assistant", Text: "bad"}}); !speecherr.IsCode(err, speecherr.CodeInvalidArgument) {
					t.Fatalf("invalid context: %v", err)
				}
			}
			err = updater.UpdateContext(ctx, messages)
			if dynamic && err != nil || !dynamic && !speecherr.IsCode(err, speecherr.CodeUnsupported) {
				t.Fatalf("context support: %v", err)
			}
			if c.Capabilities().Features.Supports(asr.FeatureDynamicContext) != dynamic {
				t.Fatal("capability and protocol differ")
			}
			if err := s.Send(ctx, []byte{0, 0}); err != nil {
				t.Fatal(err)
			}
			if e, err := s.Recv(ctx); err != nil || e.Text != "第二句" {
				t.Fatalf("second sentence = %+v, %v", e, err)
			}
			u = metadata.SessionMetadata().(SessionDetails).Usage
			if u.Final || u.TotalTokens == nil || *u.TotalTokens != 22 {
				t.Fatalf("heartbeat usage = %+v", u)
			}
			if err := s.CloseSend(ctx); err != nil {
				t.Fatal(err)
			}
			if err := updater.UpdateContext(ctx, messages); !errors.Is(err, asr.ErrInputClosed) {
				t.Fatalf("update after CloseSend: %v", err)
			}
			for range 2 {
				if _, err := s.Recv(ctx); !errors.Is(err, io.EOF) {
					t.Fatalf("end = %v", err)
				}
			}
			u = metadata.SessionMetadata().(SessionDetails).Usage
			if !u.Final || u.InputTokens == nil || *u.InputTokens != 20 ||
				u.OutputTokens == nil || *u.OutputTokens != 4 || u.TotalTokens == nil || *u.TotalTokens != 24 ||
				u.DurationSeconds == nil || *u.DurationSeconds != 2 {
				t.Fatalf("final cumulative usage = %+v", u)
			}
		})
	}
}

func TestUsageOptionalFields(t *testing.T) {
	s := &Stream{}
	s.recordUsage(json.RawMessage(`{"duration":0,"input_tokens":0,"output_tokens":null,"total_tokens":"bad"}`), false)
	u := s.SessionMetadata().(SessionDetails).Usage
	if u.DurationSeconds == nil || *u.DurationSeconds != 0 || u.InputTokens == nil || *u.InputTokens != 0 ||
		u.OutputTokens != nil || u.TotalTokens != nil || u.Final {
		t.Fatalf("usage = %+v", u)
	}
	s.recordUsage(nil, true)
	s.recordUsage(json.RawMessage(`{"duration":-1,"input_tokens":-1}`), true)
	if s.SessionMetadata().(SessionDetails).Usage.Final {
		t.Fatal("missing or malformed final usage promoted previous snapshot")
	}
	s.recordUsage(json.RawMessage(`{"duration":1.5}`), true)
	u = s.SessionMetadata().(SessionDetails).Usage
	if !u.Final || u.DurationSeconds == nil || *u.DurationSeconds != 1.5 || u.InputTokens != nil {
		t.Fatalf("duration-only usage = %+v", u)
	}
}

func TestRecognizeReturnsFinalUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		var run taskMessage
		if err := conn.ReadJSON(&run); err != nil {
			t.Error(err)
			return
		}
		send := func(event, payload string) {
			if err := conn.WriteJSON(map[string]any{
				"header": map[string]any{"event": event, "task_id": run.Header.TaskID},
				"payload": json.RawMessage(payload),
			}); err != nil {
				t.Error(err)
			}
		}
		send("task-started", "{}")
		for range 2 {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Error(err)
				return
			}
		}
		send("result-generated", `{"output":{"sentence":{"sentence_end":true,"text":"你好"}},"usage":{"duration":1,"total_tokens":12}}`)
		send("task-finished", `{"output":{},"usage":{"duration":1,"input_tokens":10,"output_tokens":3,"total_tokens":13}}`)
	}))
	defer server.Close()
	c, err := New(Config{APIKey: "test", Endpoint: "ws" + strings.TrimPrefix(server.URL, "http") + "/api-ws/v1/inference"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := c.Recognize(ctx, audio.Buffer{
		Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
		Data: make([]byte, 1280),
	}, asr.Config{})
	if err != nil {
		t.Fatal(err)
	}
	d, ok := out.Provider.(SessionDetails)
	if out.Text != "你好" || !ok || !d.Usage.Final || d.Usage.TotalTokens == nil || *d.Usage.TotalTokens != 13 {
		t.Fatalf("result = %+v", out)
	}
}
