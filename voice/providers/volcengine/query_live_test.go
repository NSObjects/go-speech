//go:build integration

package volcengine

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/voice"
)

func TestVolcengineLiveVoiceQuery(t *testing.T) {
	ref := VoiceRef{
		SpeakerID:       os.Getenv("VOLCENGINE_VOICE_SPEAKER_ID"),
		CustomSpeakerID: os.Getenv("VOLCENGINE_VOICE_CUSTOM_SPEAKER_ID"),
	}
	if ref.SpeakerID == "" && ref.CustomSpeakerID == "" {
		t.Skip("NOT VALIDATED: no existing voice slot or custom speaker ID configured")
	}
	c, err := New(Config{APIKey: os.Getenv("VOLCENGINE_API_KEY"), Endpoint: os.Getenv("VOLCENGINE_VOICE_ENDPOINT")})
	if err != nil {
		t.Fatal("invalid Volcengine voice client configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	r, err := c.Query(ctx, ref)
	if err != nil {
		// Provider diagnostics can contain resource identifiers or URLs.
		t.Fatalf("voice query failed: %T (inspect provider console)", err)
	}
	if r.Status == nil || r.State() == voice.NotFound || r.SpeakerID == "" {
		t.Fatal("configured voice was not found or status was absent")
	}
	if ref.SpeakerID != "" && r.SpeakerID != ref.SpeakerID {
		t.Fatal("voice query returned an unexpected slot")
	}
	if r.AvailableTrainingTimes != nil && *r.AvailableTrainingTimes < 0 {
		t.Fatal("invalid remaining training count")
	}
	t.Logf("voice query succeeded: state=%s native_status=%d models=%d", r.State(), *r.Status, len(r.SpeakerStatus))
}
