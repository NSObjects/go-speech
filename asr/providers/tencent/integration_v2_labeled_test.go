//go:build integration

package tencent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestTencentLiveLabeledV2 evaluates natural speech through the ordinary V2
// streaming engine. It is opt-in, potentially billable, and never uploads
// reference text or private recordings to GitHub Actions.
func TestTencentLiveLabeledV2(t *testing.T) {
	manifestPath := os.Getenv("TENCENT_V2_EVAL_MANIFEST")
	if manifestPath == "" {
		t.Skip("TENCENT_V2_EVAL_MANIFEST unset; natural speech accuracy has not been measured")
	}
	model := os.Getenv("TENCENT_V2_ASR_MODEL")
	if model == "" {
		model = "16k_zh_en_2.0"
	}
	if model != "16k_zh_en_2.0" {
		t.Fatalf("labeled V2 text benchmark requires 16k_zh_en_2.0; got %q", model)
	}
	for _, key := range []string{"TENCENT_APP_ID", "TENCENT_SECRET_ID", "TENCENT_SECRET_KEY"} {
		if strings.TrimSpace(os.Getenv(key)) == "" {
			t.Fatalf("%s must be configured for the real-cloud V2 benchmark", key)
		}
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest vadLabeledManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("invalid labeled V2 manifest: %v", err)
	}
	if strings.TrimSpace(manifest.PCMFile) == "" {
		t.Fatal("pcm_file is required")
	}
	pcmPath := manifest.PCMFile
	if !filepath.IsAbs(pcmPath) {
		pcmPath = filepath.Join(filepath.Dir(manifestPath), pcmPath)
	}
	info, err := os.Stat(pcmPath)
	if err != nil {
		t.Fatal(err)
	}
	const maxAudioBytes = 32 * 1024 * 1024
	if info.Size() > maxAudioBytes {
		t.Fatal("PCM too large (32MiB maximum); split long recordings into separate sessions")
	}
	pcm, err := os.ReadFile(pcmPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateVADLabeledManifest(manifest, len(pcm)); err != nil {
		t.Fatal(err)
	}
	duration := time.Duration(len(pcm)) * time.Second / (16000 * 2)
	ctx, cancel := context.WithTimeout(context.Background(), duration+60*time.Second)
	defer cancel()

	start := time.Now()
	result, err := liveSession(ctx, model, 16000, 0, pcm)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	score, err := scoreVADTranscript(manifest.ReferenceText, result.finals)
	if err != nil {
		t.Fatal(err)
	}
	// Aggregate only; do not log transcripts, secrets, filenames or audio.
	t.Logf("V2_LABELED model=%s audio_ms=%d char_error_rate=%.4f edits=%d reference_chars=%d finals=%d partials=%d session_wall_ms=%d",
		model, duration.Milliseconds(), score.Rate, score.EditDistance,
		score.ReferenceCharacters, len(result.finals), result.partials, elapsed.Milliseconds())
	// Passing asserts protocol success, not an accuracy threshold. Natural
	// recordings need sufficient independent speakers and acoustic conditions.
}
