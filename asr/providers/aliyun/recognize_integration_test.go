//go:build integration

package aliyun

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
)

func TestAliyunLiveShortRecognize(t *testing.T) {
	key, path, model := os.Getenv("DASHSCOPE_API_KEY"), os.Getenv("ASR_TEST_PCM_FILE"), os.Getenv("DASHSCOPE_ASR_MODEL")
	if key == "" || path == "" || model == "" {
		t.Fatal("Aliyun integration credentials, fixture, and model required")
	}
	rate := 16000
	if model == ModelParaformerRealtime8K {
		rate = 8000
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{APIKey: key, Endpoint: os.Getenv("DASHSCOPE_WS_ENDPOINT"), Model: model}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	on, off, noise := true, false, 0.0
	native := Options{}
	options := asr.Options{UtteranceDetails: true}
	switch model {
	case ModelQwenAudio31Streaming, ModelQwenAudio31Message:
		native.Context = []ContextMessage{{Role: "user", Text: "We are testing speech recognition."}}
		native.Vocabulary = map[string]int{"recognition": 5}
		native.LanguageHints = []string{"en"}
		native.SpeechNoiseThreshold = &noise
		if model == ModelQwenAudio31Message {
			native.IntermediateResults = &on
		}
	case ModelFunASRRealtime:
		native.SpeechNoiseThreshold = &noise
	case ModelParaformerRealtime, ModelParaformerRealtime8K:
		options.Punctuation = &on
		options.TextNormalization = &on
		options.DisfluencyRemoval = &off
		native.SemanticPunctuation = &off
	}
	options.Provider = native
	result, err := c.Recognize(ctx, audio.Buffer{Data: raw, Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: rate, Channels: 1}}, asr.Config{Options: options})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(result.Text) == "" {
		t.Fatalf("no final transcription for %s", model)
	}
	verifyTimedUtterances(t, result.Utterances, true)
	if model == ModelQwenAudio31Streaming || model == ModelQwenAudio31Message {
		verifyFinalTokenUsage(t, result.Provider)
	}
	// The existing 8k fixture is synthetic English; do not require a
	// particular emotion label from a Chinese telephony model.
	if model == ModelParaformerRealtime8K {
		found := false
		for _, u := range result.Utterances {
			if u.Emotion != "" {
				found = true
				d, ok := u.Provider.(UtteranceDetails)
				if !ok || d.EmotionConfidence == nil {
					t.Fatal("emotion returned without confidence")
				}
			}
		}
		if !found {
			t.Log("NOT VALIDATED: Paraformer 8k emotion absent on this fixture")
		}
	}
	t.Logf("Aliyun short Recognize model=%s characters=%d", model, len([]rune(result.Text)))
}
