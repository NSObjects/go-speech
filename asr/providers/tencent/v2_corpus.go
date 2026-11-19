package tencent

import (
	"errors"
	"fmt"
	"strings"
)

// v2CorpusManifest stores local-only references. No transcript or path is
// printed by the benchmark or committed to CI.
type v2CorpusManifest struct {
	Cases []v2CorpusCase `json:"cases"`
}

type v2CorpusCase struct {
	ID             string              `json:"id"`
	Condition      string              `json:"condition"`
	PCMFile        string              `json:"pcm_file"`
	ReferenceText  string              `json:"reference_text"`
	SpeechIntervals []vadSpeechInterval `json:"speech_intervals"`
}

type v2CorpusStats struct {
	Files      int
	Edits      int
	Characters int
}

func (s *v2CorpusStats) Add(score vadCharacterScore) {
	s.Files++
	s.Edits += score.EditDistance
	s.Characters += score.ReferenceCharacters
}

func (s v2CorpusStats) CER() float64 {
	if s.Characters == 0 {
		return 0
	}
	return float64(s.Edits) / float64(s.Characters)
}

func validateV2Corpus(c v2CorpusManifest) error {
	if len(c.Cases) == 0 || len(c.Cases) > 200 {
		return errors.New("corpus must contain 1–200 recordings")
	}
	seen := make(map[string]bool, len(c.Cases))
	for i, item := range c.Cases {
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Condition) == "" {
			return fmt.Errorf("case %d requires a nonempty id and condition", i)
		}
		if seen[item.ID] {
			return fmt.Errorf("duplicate case id at index %d", i)
		}
		seen[item.ID] = true
		if strings.TrimSpace(item.PCMFile) == "" {
			return fmt.Errorf("case %d missing pcm_file", i)
		}
		if len(normalizeASRText(item.ReferenceText)) == 0 {
			return fmt.Errorf("case %d has empty normalized reference", i)
		}
		if len(item.SpeechIntervals) == 0 {
			return fmt.Errorf("case %d missing speech intervals", i)
		}
	}
	return nil
}
