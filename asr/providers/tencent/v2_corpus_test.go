package tencent

import "testing"

func TestV2CorpusValidation(t *testing.T) {
	good := v2CorpusManifest{Cases: []v2CorpusCase{{
		ID: "clip-1", Condition: "quiet", PCMFile: "clip.pcm",
		ReferenceText: "你好", SpeechIntervals: []vadSpeechInterval{{StartMS: 0, EndMS: 1000}},
	}}}
	if err := validateV2Corpus(good); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func(*v2CorpusManifest)
	}{
		{"empty", func(c *v2CorpusManifest) { c.Cases = nil }},
		{"missing id", func(c *v2CorpusManifest) { c.Cases[0].ID = "" }},
		{"missing group", func(c *v2CorpusManifest) { c.Cases[0].Condition = "" }},
		{"missing pcm", func(c *v2CorpusManifest) { c.Cases[0].PCMFile = "" }},
		{"empty normalized reference", func(c *v2CorpusManifest) { c.Cases[0].ReferenceText = "，。" }},
		{"missing intervals", func(c *v2CorpusManifest) { c.Cases[0].SpeechIntervals = nil }},
		{"duplicate id", func(c *v2CorpusManifest) { c.Cases = append(c.Cases, c.Cases[0]) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := v2CorpusManifest{Cases: append([]v2CorpusCase(nil), good.Cases...)}
			tc.edit(&c)
			if err := validateV2Corpus(c); err == nil {
				t.Fatal("invalid corpus accepted")
			}
		})
	}
}

func TestV2CorpusWeightedCER(t *testing.T) {
	var result v2CorpusStats
	result.Add(vadCharacterScore{ReferenceCharacters: 10, EditDistance: 1, Rate: .1})
	result.Add(vadCharacterScore{ReferenceCharacters: 2, EditDistance: 2, Rate: 1})
	if result.Files != 2 || result.Characters != 12 || result.Edits != 3 || result.CER() != .25 {
		t.Fatalf("got %+v CER %f, want 2 files, 3/12 CER", result, result.CER())
	}
	if (v2CorpusStats{}).CER() != 0 {
		t.Fatal("empty summary must not divide by zero")
	}
}
