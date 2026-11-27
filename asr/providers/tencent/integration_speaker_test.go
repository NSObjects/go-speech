//go:build integration

package tencent

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
)

// This is opt-in because the speaker-enabled model has distinct entitlement.
// A single synthesized voice verifies speaker_id transport, NOT separation
// quality for a real multi-person recording.
func TestTencentLiveV2SpeakerEngine(t *testing.T) {
	model := os.Getenv("TENCENT_V2_SPEAKER_MODEL")
	if model == "" {
		t.Skip("set TENCENT_V2_SPEAKER_MODEL to verify speaker-enabled 2.0 entitlement")
	}
	if model != "16k_zh_en_speaker_2.0" {
		t.Fatalf("unsupported speaker test engine %q; require 16k_zh_en_speaker_2.0", model)
	}
	fixture := "TENCENT_TEST_SPEAKER_PCM_FILE"
	if os.Getenv(fixture) == "" {
		fixture = "TENCENT_TEST_16K_PCM_FILE"
	}
	pcm := livePCMFixture(t, fixture)
	ctx, cancel := context.WithTimeout(context.Background(), 95*time.Second)
	defer cancel()
	result, err := liveSession(ctx, model, 16000, 0, pcm)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.speakerIDs) == 0 {
		t.Fatal("speaker-engine returned no speaker_id fields; metadata transport unverified")
	}
	unknown, assigned := 0, 0
	for _, id := range result.speakerIDs {
		if id == -1 {
			unknown++
		} else if id >= 0 && id <= 9 {
			assigned++
		} else {
			t.Fatalf("Tencent speaker engine returned out-of-range speaker_id=%d", id)
		}
	}
	if assigned == 0 {
		t.Fatal("speaker engine returned only pending speaker_id=-1; no assigned speaker ID verified")
	}
	t.Logf("Tencent speaker engine=%s speaker_events=%d pending=%d assigned=%d finals=%d",
		model, len(result.speakerIDs), unknown, assigned, len(result.finals))
}

// Use only the pinned public speech fixture; do not log or persist the
// service-issued context ID. Reuse it immediately in a second task.
func TestTencentLiveV2SpeakerContext(t *testing.T) {
	pcm := livePCMFixture(t, "TENCENT_TEST_16K_PCM_FILE")
	model := "16k_zh_en_speaker_2.0"
	client, err := New(Config{
		AppID: os.Getenv("TENCENT_APP_ID"), SecretID: os.Getenv("TENCENT_SECRET_ID"),
		SecretKey: os.Getenv("TENCENT_SECRET_KEY"), EngineModel: model, NeedVAD: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Use a bounded sample long enough to emit speaker metadata; no new
	// user recording or separate fixture download is required.
	const minBytes = 12 * 16000 * 2
	var sample []byte
	for len(sample) < minBytes {
		sample = append(sample, pcm...)
	}
	if len(sample) > 30*16000*2 {
		sample = sample[:30*16000*2]
	}
	input := audio.Buffer{Data: sample, Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1}}
	on := true
	var contextID string
	for i := range 2 {
		ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
		r, err := client.Recognize(ctx, input, asr.Config{Options: asr.Options{
			UtteranceDetails: true, Provider: Options{EnableSpeakerContext: &on, SpeakerContextID: contextID},
		}})
		cancel()
		if err != nil {
			t.Fatalf("speaker context task %d: %v", i+1, err)
		}
		if r.Text == "" {
			t.Fatal("empty speaker-context transcript")
		}
		d, ok := r.Provider.(SessionDetails)
		if !ok || d.SpeakerContextID == "" {
			t.Fatal("speaker context ID absent")
		}
		contextID = d.SpeakerContextID
		assigned := 0
		for _, u := range r.Utterances {
			if u.SpeakerID != nil && *u.SpeakerID >= 0 {
				assigned++
			}
		}
		if assigned == 0 {
			t.Fatal("no assigned speaker IDs")
		}
		t.Logf("speaker context task %d: %d assigned final segments", i+1, assigned)
	}
}
