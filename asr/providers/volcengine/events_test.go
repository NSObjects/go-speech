package volcengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/gorilla/websocket"
)

func TestFullTranscriptDoesNotRepeatFinalUtterances(t *testing.T) {
	s := &Stream{resultType: ResultFull, finalSeen: make(map[string]struct{})}
	first := utterance{Text: "今天天气真好。", Definite: true, Start: 0, End: 1300}
	second := utterance{Text: "明天也不错。", Definite: true, Start: 1400, End: 2600}
	steps := []struct {
		result recognition
		last   bool
		want   []asr.Event
	}{
		{recognition{Text: "今天", Utterances: []utterance{{Text: "今天"}}}, false,
			[]asr.Event{{Type: asr.EventPartial, Text: "今天"}}},
		{recognition{Text: "今天天气", Utterances: []utterance{{Text: "今天天气"}}}, false,
			[]asr.Event{{Type: asr.EventPartial, Text: "今天天气"}}},
		{recognition{Text: "今天天气真好。", Utterances: []utterance{first}}, false,
			[]asr.Event{{Type: asr.EventFinal, Text: "今天天气真好。"}}},
		{recognition{Text: "今天天气真好。明天", Utterances: []utterance{first, {Text: "明天", Start: 1400}}}, false,
			[]asr.Event{{Type: asr.EventPartial, Text: "明天"}}},
		{recognition{Text: "今天天气真好。明天", Utterances: []utterance{first, {Text: "明天", Start: 1400}}}, false, nil},
		{recognition{Text: "今天天气真好。明天也不错。", Utterances: []utterance{first, second}}, true,
			[]asr.Event{{Type: asr.EventFinal, Text: "明天也不错。"}}},
	}
	for i, step := range steps {
		s.addEvents([]recognition{step.result}, step.last)
		if !reflect.DeepEqual(s.queue, step.want) {
			t.Fatalf("response %d: events = %#v, want %#v", i, s.queue, step.want)
		}
		s.queue = nil
	}
	if s.pendingText != "" || s.confirmedText != "今天天气真好。明天也不错。" {
		t.Fatalf("unexpected final state: pending=%q confirmed=%q", s.pendingText, s.confirmedText)
	}
}

func TestSingleResponseModeDoesNotDropIdenticalUtterances(t *testing.T) {
	s := &Stream{resultType: ResultSingle, finalSeen: make(map[string]struct{})}
	for i, start := range []int64{100, 1100} {
		s.addEvents([]recognition{{
			Text: "你好", Utterances: []utterance{
				{Text: "你好", Definite: true, Start: start, End: start + 500},
			},
		}}, i == 1)
		if len(s.queue) != 1 || s.queue[0] != (asr.Event{Type: asr.EventFinal, Text: "你好"}) {
			t.Fatalf("utterance %d: %#v", i, s.queue)
		}
		s.queue = nil
	}
}

func TestTerminalTextWithoutUtteranceMetadata(t *testing.T) {
	t.Run("unconfirmed text survives final packet", func(t *testing.T) {
		s := &Stream{resultType: ResultFull, finalSeen: make(map[string]struct{})}
		s.addEvents([]recognition{{Text: "你好"}}, false)
		if !reflect.DeepEqual(s.queue, []asr.Event{{Type: asr.EventPartial, Text: "你好"}}) {
			t.Fatalf("partial: %#v", s.queue)
		}
		s.queue = nil
		s.addEvents([]recognition{{Text: "你好世界"}}, true)
		if !reflect.DeepEqual(s.queue, []asr.Event{{Type: asr.EventFinal, Text: "你好世界"}}) {
			t.Fatalf("final: %#v", s.queue)
		}
	})
	t.Run("repeated confirmed full text does not become a second final", func(t *testing.T) {
		s := &Stream{resultType: ResultFull, finalSeen: make(map[string]struct{})}
		s.addEvents([]recognition{{Utterances: []utterance{
			{Text: "你好。", Definite: true, End: 1000},
		}}}, false)
		s.queue = nil
		s.addEvents([]recognition{{Text: "你好。"}}, true)
		if len(s.queue) != 0 {
			t.Fatalf("duplicated final result: %#v", s.queue)
		}
	})
	t.Run("unconfirmed suffix after prior sentence is preserved", func(t *testing.T) {
		s := &Stream{resultType: ResultFull, finalSeen: make(map[string]struct{})}
		s.addEvents([]recognition{{Utterances: []utterance{
			{Text: "你好。", Definite: true, End: 1000},
		}}}, false)
		s.queue = nil
		s.addEvents([]recognition{{Text: "你好。明天见"}}, true)
		if !reflect.DeepEqual(s.queue, []asr.Event{{Type: asr.EventFinal, Text: "明天见"}}) {
			t.Fatalf("missing trailing final: %#v", s.queue)
		}
	})
}

func TestNestedPayloadDoesNotDiscardEndOfStreamFlag(t *testing.T) {
	message := wireMessage{
		kind:    messageFullResponse,
		payload: []byte(`{"code":0,"is_last_package":true,"payload_msg":{"result":[{"text":"confirmed","utterances":[{"text":"confirmed","definite":true,"start_time":0,"end_time":400}]}]}}`),
	}
	results, last, noSpeech, err := message.result()
	if err != nil || !last || noSpeech || len(results) != 1 || results[0].Text != "confirmed" {
		t.Fatalf("nested result: %#v, last=%v, err=%v", results, last, err)
	}
}

func TestRecvStableTerminalStateWhenCallContextCanceledLater(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		s    *Stream
		want error
	}{
		{"finished", &Stream{finished: true}, io.EOF},
		{"failed", &Stream{terminalErr: io.ErrUnexpectedEOF}, io.ErrUnexpectedEOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for i := 0; i < 2; i++ {
				_, err := tc.s.Recv(ctx)
				if !errors.Is(err, tc.want) {
					t.Fatalf("read %d: got %v, want %v", i, err, tc.want)
				}
			}
		})
	}
}

func TestResultTypeRequestAndValidation(t *testing.T) {
	if _, err := New(Config{APIKey: "key", ResultType: "invalid"}); err == nil {
		t.Fatal("invalid result type accepted")
	}
	for _, mode := range []string{ResultFull, ResultSingle} {
		t.Run(mode, func(t *testing.T) {
			seen := make(chan error, 1)
			endpoint := mockEndpoint(t, func(ws *websocket.Conn, _ *http.Request) {
				typ, frame, err := ws.ReadMessage()
				if err != nil || typ != websocket.BinaryMessage {
					seen <- fmt.Errorf("missing config request: %v", err)
					return
				}
				kind, _, raw, err := testRequestFrame(frame)
				if err != nil || kind != messageFullRequest {
					seen <- fmt.Errorf("invalid request: %v", err)
					return
				}
				var request struct {
					Request struct {
						ResultType string `json:"result_type"`
					} `json:"request"`
				}
				if err := json.Unmarshal(raw, &request); err != nil {
					seen <- err
					return
				}
				if request.Request.ResultType != mode {
					seen <- fmt.Errorf("result type = %q; expected %q", request.Request.ResultType, mode)
					return
				}
				seen <- nil
			})
			client, err := New(Config{APIKey: "key", Endpoint: endpoint, ResultType: mode})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			stream, err := client.OpenStream(ctx, asr.Config{
				Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			select {
			case err := <-seen:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		})
	}
}
