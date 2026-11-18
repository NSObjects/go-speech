package tencent

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode"
)

type vadSpeechInterval struct {
	StartMS int64 `json:"start_ms"`
	EndMS   int64 `json:"end_ms"`
}

// vadLabeledManifest describes a locally held PCM recording with reference
// text and manually annotated speech intervals. Neither is uploaded to CI.
type vadLabeledManifest struct {
	PCMFile         string              `json:"pcm_file"`
	ReferenceText   string              `json:"reference_text"`
	SpeechIntervals []vadSpeechInterval `json:"speech_intervals"`
}

func normalizeASRText(s string) []rune {
	runes := make([]rune, 0, len(s))
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			runes = append(runes, unicode.ToLower(r))
		}
	}
	return runes
}

// runeEditDistance computes the edit distance with O(len(candidate)) storage.
// This is a character-level diagnostic (not word error rate). Punctuation,
// spaces and case are ignored; Han characters and ASCII letters are preserved.
func runeEditDistance(reference, candidate []rune) int {
	row := make([]int, len(candidate)+1)
	for j := range row {
		row[j] = j
	}
	for i, expected := range reference {
		diagonal := row[0]
		row[0] = i + 1
		for j, actual := range candidate {
			previous := row[j+1]
			deletions := row[j+1] + 1
			insertions := row[j] + 1
			substitutions := diagonal
			if expected != actual {
				substitutions++
			}
			distance := deletions
			if insertions < distance {
				distance = insertions
			}
			if substitutions < distance {
				distance = substitutions
			}
			row[j+1] = distance
			diagonal = previous
		}
	}
	return row[len(candidate)]
}

type vadCharacterScore struct {
	ReferenceCharacters int
	EditDistance        int
	Rate                float64
}

func scoreVADTranscript(reference string, finalizedSegments []string) (vadCharacterScore, error) {
	expected := normalizeASRText(reference)
	if len(expected) == 0 {
		return vadCharacterScore{}, errors.New("reference transcript contains no letters or numbers")
	}
	actual := normalizeASRText(strings.Join(finalizedSegments, ""))
	distance := runeEditDistance(expected, actual)
	return vadCharacterScore{
		ReferenceCharacters: len(expected),
		EditDistance:        distance,
		Rate:                float64(distance) / float64(len(expected)),
	}, nil
}

func validateVADLabeledManifest(m vadLabeledManifest, audioBytes int) error {
	if m.PCMFile == "" {
		return errors.New("pcm_file is required")
	}
	if _, err := scoreVADTranscript(m.ReferenceText, nil); err != nil {
		return fmt.Errorf("reference_text: %w", err)
	}
	if audioBytes < 3200 || audioBytes%2 != 0 {
		return errors.New("pcm_file must contain complete mono 16kHz PCM16LE samples")
	}
	if len(m.SpeechIntervals) == 0 {
		return errors.New("at least one manually annotated speech interval is required")
	}
	audioDurationMS := int64(audioBytes) * 1000 / (16000 * 2)
	var previousEnd int64
	for i, interval := range m.SpeechIntervals {
		if interval.StartMS < previousEnd || interval.StartMS < 0 ||
			interval.EndMS <= interval.StartMS || interval.EndMS > audioDurationMS {
			return fmt.Errorf("invalid, overlapping, or out-of-range speech interval %d", i)
		}
		previousEnd = interval.EndMS
	}
	return nil
}

func TestTencentVADCharacterScore(t *testing.T) {
	for _, tc := range []struct {
		name      string
		reference string
		segments  []string
		edits     int
		chars     int
	}{
		{name: "Chinese punctuation normalization", reference: "你好，世界！", segments: []string{"你好", "世界"}, edits: 0, chars: 4},
		{name: "one missing Han", reference: "你好世界", segments: []string{"你好", "世"}, edits: 1, chars: 4},
		{name: "one substitution", reference: "天气", segments: []string{"天器"}, edits: 1, chars: 2},
		{name: "one extra character", reference: "今天", segments: []string{"今天啊"}, edits: 1, chars: 2},
		{name: "Latin case", reference: "Hello, ASR!", segments: []string{"helloasr"}, edits: 0, chars: 8},
		{name: "empty result", reference: "你好", edits: 2, chars: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := scoreVADTranscript(tc.reference, tc.segments)
			if err != nil {
				t.Fatal(err)
			}
			if got.EditDistance != tc.edits || got.ReferenceCharacters != tc.chars ||
				got.Rate != float64(tc.edits)/float64(tc.chars) {
				t.Fatalf("character score = %+v; want edits=%d characters=%d", got, tc.edits, tc.chars)
			}
		})
	}
	if _, err := scoreVADTranscript("，！。", []string{"你好"}); err == nil {
		t.Fatal("empty normalized reference must be rejected")
	}
}

func TestTencentVADLabeledManifestValidation(t *testing.T) {
	valid := vadLabeledManifest{
		PCMFile: "local.pcm", ReferenceText: "你好",
		SpeechIntervals: []vadSpeechInterval{
			{StartMS: 200, EndMS: 900},
			{StartMS: 1200, EndMS: 1700},
		},
	}
	// 2 seconds of 16k mono PCM16LE.
	if err := validateVADLabeledManifest(valid, 64000); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*vadLabeledManifest)
		bytes  int
	}{
		{name: "missing pcm", change: func(m *vadLabeledManifest) { m.PCMFile = "" }, bytes: 64000},
		{name: "missing reference", change: func(m *vadLabeledManifest) { m.ReferenceText = "" }, bytes: 64000},
		{name: "missing intervals", change: func(m *vadLabeledManifest) { m.SpeechIntervals = nil }, bytes: 64000},
		{name: "overlap", change: func(m *vadLabeledManifest) { m.SpeechIntervals[1].StartMS = 800 }, bytes: 64000},
		{name: "past end", change: func(m *vadLabeledManifest) { m.SpeechIntervals[1].EndMS = 3000 }, bytes: 64000},
		{name: "reversed", change: func(m *vadLabeledManifest) { m.SpeechIntervals[0].EndMS = 100 }, bytes: 64000},
		{name: "partial sample", change: func(m *vadLabeledManifest) {}, bytes: 64001},
		{name: "empty audio", change: func(m *vadLabeledManifest) {}, bytes: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			modified := valid
			modified.SpeechIntervals = append([]vadSpeechInterval(nil), valid.SpeechIntervals...)
			tc.change(&modified)
			if err := validateVADLabeledManifest(modified, tc.bytes); err == nil {
				t.Fatal("invalid labeled fixture was accepted")
			}
		})
	}
}
