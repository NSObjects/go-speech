//go:build integration

package tencent

import (
	"context"
	"os"
	"testing"
	"time"
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
	pcm := livePCMFixture(t, "TENCENT_TEST_16K_PCM_FILE")
	ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
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
	t.Logf("Tencent speaker engine=%s speaker_events=%d pending=%d assigned=%d finals=%d",
		model, len(result.speakerIDs), unknown, assigned, len(result.finals))
}
