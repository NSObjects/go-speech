//go:build integration

package volcengine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
)

func TestVolcengineLiveShortRecognize(t *testing.T) {
	path := os.Getenv("VOLCENGINE_TEST_PCM_FILE")
	if path == "" {
		t.Fatal("VOLCENGINE_TEST_PCM_FILE is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(Config{
		APIKey:     os.Getenv("VOLCENGINE_API_KEY"),
		ResourceID: os.Getenv("VOLCENGINE_RESOURCE_ID"),
		Endpoint:   os.Getenv("VOLCENGINE_WS_ENDPOINT"),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	on := true
	result, err := asr.RecognizeStream(ctx, &timingTraceClient{Client: client, t: t}, audio.Buffer{
		Data: raw, Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
	}, asr.Config{Options: asr.Options{UtteranceDetails: true, Provider: Options{
		EnableNonStream: &on, ShowSpeechRate: &on, ShowVolume: &on, EnableEmotionDetection: &on,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(result.Text) == "" {
		t.Fatal("Volcengine Recognize returned no final text")
	}
	for i, u := range result.Utterances {
		t.Logf("aggregate segment=%d id=%q text=%q definite=%t start=%s end=%s words=%d rate=%t volume=%t emotion=%q",
			i, u.ID, u.Text, u.Definite, traceTime(u.StartTime), traceTime(u.EndTime), len(u.Words), u.SpeechRate != nil, u.VolumeDB != nil, u.Emotion)
	}
	verifyTimedUtterances(t, result.Utterances, true)
	rate, volume, emotion := 0, 0, 0
	for _, u := range result.Utterances {
		if u.SpeechRate != nil && *u.SpeechRate > 0 {
			rate++
		}
		if u.VolumeDB != nil {
			volume++
		}
		if u.Emotion != "" {
			emotion++
		}
	}
	if rate == 0 || volume == 0 || emotion == 0 {
		t.Fatalf("requested perception metadata missing: speech_rate=%d volume=%d emotion=%d", rate, volume, emotion)
	}
	t.Logf("Volcengine short Recognize chars=%d speech_rate=%d volume=%d emotion=%d", len([]rune(result.Text)), rate, volume, emotion)
}

// The fixture is a pinned public test recording. Trace result bodies only;
// never log credentials, connection headers, user IDs or audio bytes.
type timingTraceClient struct {
	*Client
	t *testing.T
}

func (c *timingTraceClient) OpenStream(ctx context.Context, cfg asr.Config) (asr.Stream, error) {
	stream, err := c.Client.OpenStream(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &timingTraceStream{Stream: stream.(*Stream), t: c.t}, nil
}

type timingTraceStream struct {
	*Stream
	t        *testing.T
	frames   int
	lastBody string
}

func (s *timingTraceStream) Recv(ctx context.Context) (asr.Event, error) {
	event, err := s.Stream.recv(ctx, func(m wireMessage) {
		s.frames++
		var p serverPayload
		if json.Unmarshal(m.payload, &p) != nil {
			return
		}
		if len(p.Result) == 0 && len(p.PayloadMsg) != 0 {
			var nested serverPayload
			if json.Unmarshal(p.PayloadMsg, &nested) == nil {
				p.Result = nested.Result
			}
		}
		body := string(p.Result)
		last := p.IsLastPackage || m.flags&flagLast != 0
		if body != "" && (body != s.lastBody || last) {
			s.lastBody = body
			s.t.Logf("wire frame=%d last=%t result=%s", s.frames, last, body)
		}
	})
	if err == nil && event.Utterance != nil {
		u := event.Utterance
		s.t.Logf("event kind=%v id=%q text=%q definite=%t start=%s end=%s words=%d", event.Type, u.ID, u.Text, u.Definite, traceTime(u.StartTime), traceTime(u.EndTime), len(u.Words))
	}
	return event, err
}

func traceTime(v *time.Duration) string {
	if v == nil {
		return "missing"
	}
	return fmt.Sprintf("%dms", v.Milliseconds())
}
