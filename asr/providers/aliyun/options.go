package aliyun

import (
	"encoding/json"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/NSObjects/go-speech/asr"
)

const (
	minNoiseThreshold = -1
	maxNoiseThreshold = 1
)

// Options overrides constructor settings for one recognition task. Nil
// pointers and nil Vocabulary preserve the constructor defaults. An empty
// non-nil Vocabulary clears the constructor's weighted vocabulary.
type Options struct {
	VocabularyID         *string
	Vocabulary           map[string]int
	MaxSentenceSilenceMs *int
	Heartbeat            *bool
	IntermediateResults  *bool
	KeepDialect          *bool
	VADModel             *string
	LanguageHints        []string

	SemanticPunctuation  *bool
	MultiThresholdMode   *bool
	SpeechNoiseThreshold *float64
	SensitiveWords       *SensitiveWordsFilter
	Context              []ContextMessage
}

func (Options) ASRProviderOptions() {}

// ContextMessage is a previous user or assistant message. The SDK selects
// the native content type from Role and preserves caller-supplied order.
type ContextMessage struct {
	Role string
	Text string
}

type SensitiveWordsFilter struct {
	SystemReservedFilter *bool
	FilterWithSigned     []string
	FilterWithEmpty      []string
}

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
		return Options{}, badArgument("nil provider options")
	default:
		return Options{}, badArgument("provider options do not belong to aliyun")
	}
}

func (c Config) withOverrides(n Options, o asr.Options) Config {
	if n.VocabularyID != nil {
		c.VocabularyID = *n.VocabularyID
	}
	if n.Vocabulary != nil {
		c.Vocabulary = n.Vocabulary
	}
	if n.MaxSentenceSilenceMs != nil {
		c.MaxSentenceSilenceMs = *n.MaxSentenceSilenceMs
	}
	if n.Heartbeat != nil {
		c.Heartbeat = *n.Heartbeat
	}
	if n.IntermediateResults != nil {
		c.IntermediateResults = *n.IntermediateResults
	}
	if n.KeepDialect != nil {
		c.KeepDialect = *n.KeepDialect
	}
	if n.VADModel != nil {
		c.VADModel = *n.VADModel
	}
	if o.DisfluencyRemoval != nil {
		c.DisfluencyRemoval = *o.DisfluencyRemoval
	}
	return c
}

func applyAdvanced(f modelFamily, model string, silence int, n Options, o asr.Options, language string, params map[string]any) error {
	qwen := f.supportsQwenControls()
	para := f == familyParaformer
	for _, field := range []struct {
		key string
		val *bool
		ok  bool
	}{
		{"heartbeat", n.Heartbeat, true},
		{"intermediate_result_enabled", n.IntermediateResults, f == familyQwenMessage},
		{"keep_dialect", n.KeepDialect, qwen},
		{"disfluency_removal_enabled", o.DisfluencyRemoval, f.supportsDisfluency()},
		{"punctuation_prediction_enabled", o.Punctuation, para},
		{"inverse_text_normalization_enabled", o.TextNormalization, para},
		{"semantic_punctuation_enabled", n.SemanticPunctuation, para},
		{"multi_threshold_mode_enabled", n.MultiThresholdMode, f.supportsMultiThreshold()},
	} {
		if field.val != nil {
			if !field.ok {
				return unsupported(field.key + " is not supported by " + model)
			}
			params[field.key] = *field.val
		}
	}
	if n.SemanticPunctuation != nil && *n.SemanticPunctuation {
		if silence != 0 || (n.MultiThresholdMode != nil && *n.MultiThresholdMode) {
			return badArgument("semantic segmentation conflicts with explicit VAD silence or multi-threshold mode")
		}
	}
	if n.VADModel != nil && !qwen {
		return unsupported("VADModel requires Qwen-Audio")
	}
	if v := n.SpeechNoiseThreshold; v != nil {
		if para {
			return unsupported("speech_noise_threshold is not documented for Paraformer")
		}
		if math.IsNaN(*v) || *v < minNoiseThreshold || *v > maxNoiseThreshold {
			return badArgument("SpeechNoiseThreshold must be within [-1, 1]")
		}
		params["speech_noise_threshold"] = *v
	}
	if n.LanguageHints != nil {
		if language != "" {
			return badArgument("Language and LanguageHints are mutually exclusive")
		}
		if len(n.LanguageHints) == 0 || len(n.LanguageHints) > 4 {
			return badArgument("LanguageHints must contain 1 to 4 codes")
		}
		for _, hint := range n.LanguageHints {
			if hint == "" || !utf8.ValidString(hint) || strings.TrimSpace(hint) != hint {
				return badArgument("invalid language hint")
			}
		}
		params["language_hints"] = n.LanguageHints
	}
	if v := n.SensitiveWords; v != nil {
		if !f.supportsSensitiveWords() {
			return unsupported("special_word_filter is not documented for this model")
		}
		if len(v.FilterWithSigned)+len(v.FilterWithEmpty) > 32 {
			return badArgument("at most 32 sensitive words are supported")
		}
		wire := make(map[string]any)
		if v.SystemReservedFilter != nil {
			wire["system_reserved_filter"] = *v.SystemReservedFilter
		}
		for key, list := range map[string][]string{"filter_with_signed": v.FilterWithSigned, "filter_with_empty": v.FilterWithEmpty} {
			if list == nil {
				continue
			}
			for _, word := range list {
				if !utf8.ValidString(word) || strings.TrimSpace(word) == "" {
					return badArgument("invalid sensitive word")
				}
			}
			wire[key] = map[string]any{"word_list": list}
		}
		b, err := json.Marshal(wire)
		if err != nil {
			return err
		}
		params["special_word_filter"] = string(b)
	}
	if vocabulary, ok := params["vocabulary"].(map[string]int); ok {
		super := 0
		for _, weight := range vocabulary {
			if weight == 50 {
				super++
			}
		}
		if len(vocabulary) > 2000 || super > 50 {
			return badArgument("instant vocabulary exceeds 2000 words or 50 super hotwords")
		}
	}
	return nil
}

func (c Config) contextInput(messages []ContextMessage) (map[string]any, error) {
	input := make(map[string]any)
	if len(messages) == 0 {
		return input, nil
	}
	f, err := modelType(c.Model)
	if err != nil {
		return nil, err
	}
	if !f.supportsContext() {
		return nil, unsupported("context is not supported by Paraformer")
	}
	var wire []map[string]any
	users, assistants, turnLength := 0, 0, 0
	lastRole := ""
	for _, m := range messages {
		if !utf8.ValidString(m.Text) || strings.TrimSpace(m.Text) == "" {
			return nil, badArgument("invalid context text")
		}
		contentType := "input_text"
		switch m.Role {
		case "user":
			users++
			turnLength = 0
		case "assistant":
			if lastRole != "user" {
				return nil, badArgument("assistant context must follow a user message")
			}
			assistants++
			contentType = "text"
		default:
			return nil, badArgument("context role must be user or assistant")
		}
		turnLength += utf8.RuneCountInString(m.Text)
		if users > 5 || assistants > 5 || turnLength > 400 {
			return nil, badArgument("context exceeds five messages per role or 400 characters per turn")
		}
		wire = append(wire, map[string]any{
			"role": m.Role,
			"content": []map[string]string{{"type": contentType, "text": m.Text}},
		})
		lastRole = m.Role
	}
	input["context"] = wire
	return input, nil
}

func (f modelFamily) supportsQwenControls() bool {
	return f == familyQwenStreaming || f == familyQwenMessage
}

func (f modelFamily) supportsDisfluency() bool {
	return f == familyQwenMessage || f == familyParaformer
}

func (f modelFamily) supportsMultiThreshold() bool {
	return f != familyQwenMessage
}

func (f modelFamily) supportsSensitiveWords() bool {
	return f == familyQwenStreaming || f == familyFun
}

func (f modelFamily) supportsContext() bool {
	return f != familyParaformer
}
