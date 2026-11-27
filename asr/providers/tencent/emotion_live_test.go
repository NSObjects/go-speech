//go:build integration

package tencent

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
)

// TestTencentLiveEmotionMetadata probes the billable legacy emotion option
// against the pinned public PCM fixture. It does not evaluate label accuracy
// or require a neutral recording to contain an emotional utterance.
func TestTencentLiveEmotionMetadata(t *testing.T) {
	raw, err := os.ReadFile(os.Getenv("TENCENT_TEST_16K_PCM_FILE"))
	if err != nil || len(raw) < 3200 || len(raw)%2 != 0 {
		t.Fatalf("invalid 16k PCM fixture: bytes=%d err=%v", len(raw), err)
	}
	client, err := New(Config{
		AppID: os.Getenv("TENCENT_APP_ID"), SecretID: os.Getenv("TENCENT_SECRET_ID"),
		SecretKey: os.Getenv("TENCENT_SECRET_KEY"), EngineModel: "16k_zh_en", NeedVAD: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	mode := 1 // Native metadata; do not insert labels into the transcript.
	result, err := client.Recognize(ctx, audio.Buffer{
		Data: raw, Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
	}, asr.Config{Options: asr.Options{
		UtteranceDetails: true,
		Provider:         Options{EmotionRecognition: &mode},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(result.Text) == "" || len(result.Utterances) == 0 {
		t.Fatal("emotion-enabled recognition returned no final transcript or segments")
	}
	seen := make(map[string]bool)
	emotionFields := 0
	for _, u := range result.Utterances {
		details, ok := u.Provider.(UtteranceDetails)
		if !ok || len(details.RawResult) == 0 {
			t.Fatal("missing native final result")
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(details.RawResult, &fields); err != nil {
			t.Fatal("invalid native result JSON")
		}
		keys := make([]string, 0, len(fields))
		for key := range fields {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value := strings.TrimSpace(string(fields[key]))
			if value == "" {
				continue
			}
			shape := "scalar"
			switch value[0] {
			case '{':
				shape = "object"
			case '[':
				shape = "array"
			case '"':
				shape = "string"
			case 'n':
				shape = "null"
			case 't', 'f':
				shape = "boolean"
			default:
				shape = "number"
			}
			signature := key + ":" + shape
			if !seen[signature] {
				seen[signature] = true
				t.Logf("native final field %q type=%s", key, shape)
			}
			// The checked-in workflow uses public audio. Do not print the
			// transcript, raw result, credentials, or arbitrary field values.
			if strings.Contains(strings.ToLower(key), "emotion") {
				emotionFields++
			}
		}
	}
	t.Logf("emotion-enabled ASR request succeeded: segments=%d emotion-named fields=%d", len(result.Utterances), emotionFields)
	// Field presence alone does not establish the label schema or accuracy.
	t.Log("NOT VALIDATED: emotional label interpretation requires inspection of the native schema")
}
