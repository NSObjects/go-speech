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

// TestTencentLiveLabeledVAD is an opt-in, LOCAL real-recording diagnostic.
// It never sends a voice file or reference transcript to GitHub. Only its PCM
// bytes are streamed to the explicitly configured Tencent ASR account.
// All reported metrics are aggregate and contain no transcript text.
func TestTencentLiveLabeledVAD(t *testing.T) {
	manifestPath := os.Getenv("TENCENT_VAD_EVAL_MANIFEST")
	if manifestPath == "" {
		t.Skip("TENCENT_VAD_EVAL_MANIFEST unset; natural-recording VAD quality is not validated by CI")
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest vadLabeledManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("invalid VAD manifest JSON: %v", err)
	}
	if strings.TrimSpace(manifest.PCMFile) == "" {
		t.Fatal("pcm_file is required in the VAD manifest")
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
		t.Fatal("PCM recording is too large for a single evaluation (limit 32MiB); split it into shorter sessions")
	}
	pcm, err := os.ReadFile(pcmPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateVADLabeledManifest(manifest, len(pcm)); err != nil {
		t.Fatal(err)
	}
	audioDuration := time.Duration(len(pcm)) * time.Second / (16000 * 2)
	lastSpeechEnd := time.Duration(manifest.SpeechIntervals[len(manifest.SpeechIntervals)-1].EndMS) * time.Millisecond
	t.Logf("VAD_LABELED_FIXTURE audio_ms=%d reference_intervals=%d reference_chars=%d",
		audioDuration.Milliseconds(), len(manifest.SpeechIntervals),
		len(normalizeASRText(manifest.ReferenceText)))
	for _, threshold := range []time.Duration{700 * time.Millisecond, time.Second, 1500 * time.Millisecond} {
		// Independent session per setting. Always keep context alive for the
		// entire stream (OpenStream's context controls its lifetime).
		ctx, cancel := context.WithTimeout(context.Background(), audioDuration+60*time.Second)
		result, err := runTencentVADComparison(ctx, pcm, threshold)
		cancel()
		if err != nil {
			t.Fatalf("VAD=%s recording evaluation: %v", threshold, err)
		}
		score, err := scoreVADTranscript(manifest.ReferenceText, result.finalTexts)
		if err != nil {
			t.Fatal(err)
		}
		finalsDuringAnnotatedSpeech := 0
		for _, at := range result.finalOffsets {
			for _, speech := range manifest.SpeechIntervals {
				if at >= time.Duration(speech.StartMS)*time.Millisecond &&
					at < time.Duration(speech.EndMS)*time.Millisecond {
					finalsDuringAnnotatedSpeech++
					break
				}
			}
		}
		lastFinalRelative := result.finalOffsets[len(result.finalOffsets)-1] - lastSpeechEnd
		t.Logf("VAD_LABELED threshold_ms=%d char_error_rate=%.4f edits=%d reference_chars=%d finals=%d partials=%d finals_during_annotated_speech=%d last_final_relative_to_last_speech_end_ms=%d session_wall_ms=%d",
			threshold.Milliseconds(), score.Rate, score.EditDistance,
			score.ReferenceCharacters, len(result.finalOffsets), result.partials,
			finalsDuringAnnotatedSpeech, lastFinalRelative.Milliseconds(), result.wallElapsed.Milliseconds())
	}
	// An emitted Final during an annotation is not necessarily an early cut:
	// it may finalize a previous phrase. This diagnostic does not claim
	// rigorous event-to-utterance alignment or a premature-cut rate.
}
