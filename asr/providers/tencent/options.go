package tencent

import (
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/NSObjects/go-speech/asr"
)

const (
	minNoiseThreshold = -2
	maxNoiseThreshold = 2
)

// Options provides native request-local controls. Nil pointers preserve defaults.
// Numeric modes use Tencent's documented units and enums.
type Options struct {
	NeedVAD              *bool
	VADSilenceTime       *time.Duration
	MaxSpeakTime         *time.Duration
	HotwordID            *string
	Hotwords             []Hotword
	CustomizationID      string
	ReplaceTextID        string
	FilterDirty          *int
	FilterModal          *int
	FilterPunctuation    *bool // sentence-final full stop only
	ReturnEmptyResults   *bool
	ConvertNumMode       *int
	WordInfo             *int
	NoiseThreshold       *float64
	EmotionRecognition   *int // 0 off, 1 metadata, 2 labels in text; billable
	Domain               *int
	SentenceStrategy     *int
	EnableSpeakerContext *bool
	SpeakerContextID     string
}

// Hotword is a temporary weighted vocabulary entry. Order is preserved.
type Hotword struct {
	Word   string
	Weight int
}

func (Options) ASRProviderOptions() {}

func nativeOptions(o asr.Options) (Options, error) {
	switch v := o.Provider.(type) {
	case nil:
		return Options{}, nil
	case Options:
		return v, nil
	case *Options:
		if v != nil {
			return *v, nil
		}
		return Options{}, argumentError("nil provider options")
	default:
		return Options{}, argumentError("provider options do not belong to tencent")
	}
}

func (c Config) withOverrides(n Options) Config {
	if n.NeedVAD != nil {
		c.NeedVAD = *n.NeedVAD
	}
	if n.VADSilenceTime != nil {
		c.VADSilenceTime = *n.VADSilenceTime
	}
	if n.MaxSpeakTime != nil {
		c.MaxSpeakTime = *n.MaxSpeakTime
	}
	if n.HotwordID != nil {
		c.HotwordID = *n.HotwordID
	}
	return c
}

func enabledEmotion(n Options) bool {
	return n.EmotionRecognition != nil && *n.EmotionRecognition != 0
}

func (c Config) requestOptions(req asr.Config, n Options) (url.Values, string, error) {
	q := make(url.Values)
	engine := c.EngineModel
	v2 := isV2Engine(engine)
	speaker := isSpeakerEngine(engine)
	mandarin := isMandarinEngine(engine)
	if req.Options.Punctuation != nil || req.Options.TextNormalization != nil {
		return nil, "", unsupportedError("use FilterPunctuation and ConvertNumMode for Tencent's narrower native controls")
	}
	if req.Options.DisfluencyRemoval != nil {
		if n.FilterModal != nil {
			return nil, "", requestError("DisfluencyRemoval and FilterModal are mutually exclusive")
		}
		mode := 0
		if *req.Options.DisfluencyRemoval {
			mode = 1
		}
		n.FilterModal = &mode
	}
	for key, value := range map[string]string{
		"hotword_id": c.HotwordID, "customization_id": n.CustomizationID,
		"replace_text_id": n.ReplaceTextID, "speaker_context_id": n.SpeakerContextID,
	} {
		if value == "" {
			continue
		}
		if len(value) > 256 || strings.ContainsAny(value, "&?=/# \t\r\n") {
			return nil, "", requestError("invalid " + key)
		}
		q.Set(key, value)
	}
	if n.SpeakerContextID != "" && !speaker {
		return nil, "", unsupportedError("speaker context requires the speaker 2.0 engine")
	}
	if n.EnableSpeakerContext != nil {
		if !speaker {
			return nil, "", unsupportedError("speaker context requires the speaker 2.0 engine")
		}
		q.Set("enable_speaker_context", boolMode(*n.EnableSpeakerContext))
	}
	if n.SentenceStrategy != nil {
		if !v2 {
			return nil, "", unsupportedError("SentenceStrategy requires a 2.0 sentence engine")
		}
		if *n.SentenceStrategy < 0 || *n.SentenceStrategy > 1 {
			return nil, "", requestError("SentenceStrategy must be 0 or 1")
		}
		q.Set("sentence_strategy", strconv.Itoa(*n.SentenceStrategy))
	}
	for _, field := range []struct {
		key string
		value *int
		max int
	}{
		{"filter_dirty", n.FilterDirty, 2},
		{"filter_modal", n.FilterModal, 2},
		{"domain", n.Domain, 3},
	} {
		if field.value == nil {
			continue
		}
		if *field.value < 0 || *field.value > field.max {
			return nil, "", requestError("invalid " + field.key)
		}
		if field.key != "domain" && !mandarin {
			return nil, "", unsupportedError(field.key + " requires a documented Mandarin engine")
		}
		q.Set(field.key, strconv.Itoa(*field.value))
	}
	if n.FilterPunctuation != nil {
		if v2 || !mandarin {
			return nil, "", unsupportedError("FilterPunctuation requires a legacy Mandarin engine")
		}
		q.Set("filter_punc", boolMode(*n.FilterPunctuation))
	}
	if n.ReturnEmptyResults != nil {
		if v2 {
			return nil, "", unsupportedError("empty slice pairing is only implemented for the legacy response layout")
		}
		q.Set("filter_empty_result", boolMode(!*n.ReturnEmptyResults))
	}
	if n.ConvertNumMode != nil {
		if !mandarin {
			return nil, "", unsupportedError("ConvertNumMode requires a documented Mandarin engine")
		}
		switch *n.ConvertNumMode {
		case 0, 1, 3:
			q.Set("convert_num_mode", strconv.Itoa(*n.ConvertNumMode))
		default:
			return nil, "", requestError("ConvertNumMode must be 0, 1 or 3")
		}
	}
	if n.NoiseThreshold != nil {
		if math.IsNaN(*n.NoiseThreshold) || *n.NoiseThreshold < minNoiseThreshold || *n.NoiseThreshold > maxNoiseThreshold {
			return nil, "", requestError("NoiseThreshold must be within [-2, 2]")
		}
		q.Set("noise_threshold", strconv.FormatFloat(*n.NoiseThreshold, 'f', -1, 64))
	}
	if n.WordInfo != nil {
		mode := *n.WordInfo
		if mode != 0 && mode != 1 && mode != 2 && mode != 100 {
			return nil, "", requestError("WordInfo must be 0, 1, 2 or 100")
		}
		modes := wordInfoModes(engine)
		if len(modes) == 0 {
			return nil, "", unsupportedError("WordInfo is not documented for this engine")
		}
		if !slices.Contains(modes, mode) {
			return nil, "", unsupportedError("WordInfo=100 requires 16k_zh_en")
		}
		q.Set("word_info", strconv.Itoa(mode))
	}
	if n.EmotionRecognition != nil {
		if *n.EmotionRecognition < 0 || *n.EmotionRecognition > 2 {
			return nil, "", requestError("EmotionRecognition must be 0, 1 or 2")
		}
		if !supportsEmotion(engine) {
			return nil, "", unsupportedError("EmotionRecognition is not documented for this engine")
		}
		if enabledEmotion(n) && !c.NeedVAD {
			return nil, "", requestError("EmotionRecognition requires NeedVAD")
		}
		q.Set("emotion_recognition", strconv.Itoa(*n.EmotionRecognition))
	}
	if c.HotwordID != "" && (len(req.Hints) > 0 || len(n.Hotwords) > 0) {
		return nil, "", requestError("HotwordID and temporary hotwords cannot be used together")
	}
	hotwords, err := weightedHints(engine, req.Hints, n.Hotwords)
	return q, hotwords, err
}

func boolMode(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

func weightedHints(engine string, hints []string, native []Hotword) (string, error) {
	// Reuse the existing validation and deduplication contract for common hints.
	if len(native) == 0 {
		return encodeHints(hints)
	}
	if len(hints)+len(native) > 128 {
		return "", requestError("Tencent accepts at most 128 temporary hotwords")
	}
	entries := make([]Hotword, 0, len(hints)+len(native))
	index := make(map[string]int)
	for _, h := range hints {
		if _, exists := index[h]; !exists {
			index[h] = len(entries)
			entries = append(entries, Hotword{Word: h, Weight: 5})
		}
	}
	seen := make(map[string]bool)
	for _, h := range native {
		if seen[h.Word] {
			return "", requestError("duplicate native hotword")
		}
		seen[h.Word] = true
		if i, ok := index[h.Word]; ok {
			entries[i] = h
		} else {
			index[h.Word] = len(entries)
			entries = append(entries, h)
		}
	}
	out := make([]string, 0, len(entries))
	for _, h := range entries {
		if _, err := encodeHints([]string{h.Word}); err != nil {
			return "", err
		}
		if h.Weight < 1 || (h.Weight > 11 && h.Weight != 100) {
			return "", requestError("hotword weight must be 1..11 or 100")
		}
		if h.Weight == 100 && engine != "8k_zh" && engine != "16k_zh" && engine != "16k_zh_en_2.0" && engine != "16k_zh_en_speaker_2.0" {
			return "", unsupportedError("weight 100 is not documented for this engine")
		}
		out = append(out, h.Word+"|"+strconv.Itoa(h.Weight))
	}
	return strings.Join(out, ","), nil
}

func isSpeakerEngine(engine string) bool {
	return engine == "16k_zh_en_speaker_2.0"
}

func isV2Engine(engine string) bool {
	return engine == "16k_zh_en_2.0" || isSpeakerEngine(engine)
}

func isMandarinEngine(engine string) bool {
	return isV2Engine(engine) || engine == "8k_zh" || engine == "8k_zh_large" || engine == "8k_zh_finance" || engine == "16k_zh" || engine == "16k_zh_en"
}

func wordInfoModes(engine string) []int {
	switch engine {
	case "16k_zh_en":
		return []int{0, 1, 2, 100}
	case "8k_en", "8k_zh", "8k_zh_finance", "16k_zh", "16k_en", "16k_ca", "16k_zh-TW", "16k_ja", "8k_zh_large":
		return []int{0, 1, 2}
	default:
		return nil
	}
}

func supportsEmotion(engine string) bool {
	switch engine {
	case "16k_zh", "16k_zh_en", "8k_zh", "8k_zh_large":
		return true
	default:
		return false
	}
}
