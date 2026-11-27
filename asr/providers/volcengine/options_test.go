package volcengine

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/gorilla/websocket"
)

func TestPerRequestPerception(t *testing.T) {
	on, off := true, false
	window := 650 * time.Millisecond
	score := 0
	endpoint := mockEndpoint(t, func(ws *websocket.Conn, _ *http.Request) {
		_, frame, err := ws.ReadMessage()
		if err != nil {
			t.Error(err)
			return
		}
		_, _, payload, err := testRequestFrame(frame)
		if err != nil {
			t.Error(err)
			return
		}
		var req struct {
			Request map[string]any `json:"request"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			t.Error(err)
			return
		}
		for key, want := range map[string]any{
			"enable_punc": false, "enable_itn": false, "enable_ddc": true,
			"enable_nonstream": true, "result_type": "single", "end_window_size": float64(650),
			"enable_emotion_detection": true, "show_speech_rate": true, "show_volume": true,
			"enable_lid": true, "enable_gender_detection": true, "enable_speaker_info": true,
			"ssd_version": "200", "enable_accelerate_text": true, "accelerate_score": float64(0),
			"output_zh_variant": "tw", "show_utterances": true,
		} {
			if req.Request[key] != want {
				t.Errorf("%s = %v, want %v", key, req.Request[key], want)
			}
		}
		for {
			_, frame, err := ws.ReadMessage()
			if err != nil {
				t.Error(err)
				return
			}
			_, flags, _, err := testRequestFrame(frame)
			if err != nil {
				t.Error(err)
				return
			}
			if flags&flagLast != 0 {
				break
			}
		}
		serverResult(t, ws, true, json.RawMessage(`{"result":{"text":"你好","utterances":[{"text":"你好","definite":true,"start_time":0,"end_time":500,"words":[{"text":"你","start_time":0,"end_time":200},{"text":"好","start_time":200,"end_time":500}],"additions":{"emotion":"happy","speech_rate":"4.5","volume":0,"lid_lang":"speech_mand","gender":"female","future_field":{"a":1}}}]}}`))
	})
	c, err := New(Config{APIKey: "test", Endpoint: endpoint, EndWindowSize: 800 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg := asr.Config{
		Options: asr.Options{
			Punctuation: &off, TextNormalization: &off, DisfluencyRemoval: &on,
			Provider: Options{
				EnableNonStream: &on, ResultType: ResultSingle, EndWindowSize: &window,
				EnableEmotionDetection: &on, ShowSpeechRate: &on, ShowVolume: &on,
				EnableLID: &on, EnableGenderDetection: &on, EnableSpeakerInfo: &on,
				EnableAccelerateText: &on, AccelerateScore: &score, OutputZHVariant: "tw",
			},
		},
	}
	input := audio.Buffer{
		Data:   make([]byte, 1280),
		Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
	}
	result, err := c.Recognize(ctx, input, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "你好" || len(result.Utterances) != 1 {
		t.Fatalf("result = %+v", result)
	}
	u := result.Utterances[0]
	if !u.Definite || u.StartTime == nil || *u.StartTime != 0 || u.EndTime == nil || *u.EndTime != 500*time.Millisecond ||
		u.Emotion != "happy" || u.SpeechRate == nil || *u.SpeechRate != 4.5 || u.VolumeDB == nil || *u.VolumeDB != 0 ||
		len(u.Words) != 2 || u.Words[0].EndTime == nil || *u.Words[0].EndTime != 200*time.Millisecond {
		t.Fatalf("utterance = %+v", u)
	}
	d, ok := u.Provider.(UtteranceDetails)
	if !ok || d.LanguageLabel != "speech_mand" || d.GenderLabel != "female" || string(d.Additions["future_field"]) != `{"a":1}` {
		t.Fatalf("details = %+v", u.Provider)
	}
	if c.cfg.ResultType != ResultFull || c.cfg.EnableNonStream || c.cfg.EndWindowSize != 800*time.Millisecond {
		t.Fatal("request mutated shared client defaults")
	}
}

func TestDetailsPendingFinalAndMissingValues(t *testing.T) {
	var r recognition
	if err := json.Unmarshal([]byte(`{"utterances":[{"text":"hello","words":[{"text":"hello"}],"additions":{"speech_rate":"not-a-number","volume":null}}]}`), &r); err != nil {
		t.Fatal(err)
	}
	s := &Stream{resultType: ResultFull, finalSeen: make(map[string]struct{}), details: true}
	s.addEvents([]recognition{r}, false)
	s.addEvents(nil, true)
	if len(s.queue) != 2 || s.queue[1].Type != asr.EventFinal || s.queue[1].Utterance == nil {
		t.Fatalf("queue = %+v", s.queue)
	}
	u := s.queue[1].Utterance
	if u.Definite || u.StartTime != nil || u.EndTime != nil || u.SpeechRate != nil || u.VolumeDB != nil || len(u.Words) != 1 || u.Words[0].StartTime != nil {
		t.Fatalf("missing values incorrectly populated: %+v", u)
	}
}

func TestRequestOptionValidation(t *testing.T) {
	on := true
	short := 100 * time.Millisecond
	score := 21
	c := Config{ResultType: ResultFull}
	for _, o := range []Options{
		{ResultType: "invalid"},
		{EndWindowSize: &short},
		{EnableSpeakerInfo: &on},
		{EnableAccelerateText: &on, AccelerateScore: &score},
		{OutputZHVariant: "invalid"},
	} {
		if _, _, err := c.applyOptions(asr.Options{Provider: o}, make(map[string]any)); err == nil {
			t.Errorf("invalid options accepted: %+v", o)
		}
	}
}
