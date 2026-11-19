//go:build integration

package tencent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// TestTencentLiveV2Corpus runs real, potentially billable recognition for a
// local annotated corpus. It emits aggregate counts only: never audio,
// transcripts, paths or participant identifiers.
func TestTencentLiveV2Corpus(t *testing.T) {
	manifestPath := os.Getenv("TENCENT_V2_CORPUS_MANIFEST")
	if manifestPath == "" {
		t.Skip("TENCENT_V2_CORPUS_MANIFEST unset; no real-speech benchmark has been run")
	}
	model := os.Getenv("TENCENT_V2_ASR_MODEL")
	if model == "" {
		model = "16k_zh_en_2.0"
	}
	if model != "16k_zh_en_2.0" {
		t.Fatal("V2 corpus benchmark requires ordinary 16k_zh_en_2.0")
	}
	for _, key := range []string{"TENCENT_APP_ID", "TENCENT_SECRET_ID", "TENCENT_SECRET_KEY"} {
		if os.Getenv(key) == "" {
			t.Fatalf("missing %s", key)
		}
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal("cannot read local corpus manifest")
	}
	var corpus v2CorpusManifest
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal("invalid corpus JSON")
	}
	if err := validateV2Corpus(corpus); err != nil {
		t.Fatal(err)
	}
	// Preflight ALL recordings before any billable cloud request.
	const maxBytes = 32 * 1024 * 1024
	const maxTotalBytes = 200 * 1024 * 1024
	var totalBytes int64
	for i, item := range corpus.Cases {
		path := item.PCMFile
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(manifestPath), path)
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("case %d: PCM is missing or not a regular file", i)
		}
		if info.Size() > maxBytes {
			t.Fatalf("case %d: PCM exceeds 32MiB", i)
		}
		if err := validateVADLabeledManifest(vadLabeledManifest{
			PCMFile: item.PCMFile, ReferenceText: item.ReferenceText,
			SpeechIntervals: item.SpeechIntervals,
		}, int(info.Size())); err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		totalBytes += info.Size()
		if totalBytes > maxTotalBytes {
			t.Fatal("corpus exceeds total 200MiB limit")
		}
	}
	var all v2CorpusStats
	byCondition := map[string]v2CorpusStats{}
	for i, item := range corpus.Cases {
		path := item.PCMFile
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(manifestPath), path)
		}
		pcm, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("case %d: failed to read PCM", i)
		}
		duration := time.Duration(len(pcm)) * time.Second / 32000
		ctx, cancel := context.WithTimeout(context.Background(), duration+60*time.Second)
		result, err := liveSession(ctx, model, 16000, 0, pcm)
		cancel()
		if err != nil {
			t.Fatalf("case %d: ASR failed: %v", i, err)
		}
		score, err := scoreVADTranscript(item.ReferenceText, result.finals)
		if err != nil {
			t.Fatalf("case %d: scoring failed", i)
		}
		all.Add(score)
		group := byCondition[item.Condition]
		group.Add(score)
		byCondition[item.Condition] = group
		// Each individual result is numerical, with no private text.
		t.Logf("V2_CORPUS_CASE index=%d audio_ms=%d cer=%.4f edits=%d chars=%d finals=%d partials=%d",
			i, duration.Milliseconds(), score.Rate, score.EditDistance,
			score.ReferenceCharacters, len(result.finals), result.partials)
	}
	t.Logf("V2_CORPUS_TOTAL files=%d edits=%d chars=%d weighted_cer=%.4f", all.Files, all.Edits, all.Characters, all.CER())
	// Group indices map to lexically sorted condition labels from the local
	// manifest; no participant details or arbitrary labels enter cloud logs.
	labels := make([]string, 0, len(byCondition))
	for label := range byCondition {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	for idx, label := range labels {
		group := byCondition[label]
		t.Logf("V2_CORPUS_GROUP index=%d files=%d weighted_cer=%.4f edits=%d chars=%d",
			idx, group.Files, group.CER(), group.Edits, group.Characters)
	}
}
