package aliyun

import (
	"errors"
	"fmt"
	"strings"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/speecherr"
)

// Alibaba Cloud DashScope ASR models supported by this adapter.
const (
	ModelQwenAudio31Streaming = "qwen-audio-3.1-asr-flash-streaming"
	ModelQwenAudio31Message   = "qwen-audio-3.1-asr-flash-message"
	ModelFunASRRealtime       = "fun-asr-realtime"
	ModelParaformerRealtime   = "paraformer-realtime-v2"
	ModelParaformerRealtime8K = "paraformer-realtime-8k-v2"
)

type modelFamily uint8

const (
	familyQwenStreaming modelFamily = iota
	familyQwenMessage
	familyFun
	familyParaformer
)

func modelType(model string) (modelFamily, error) {
	switch model {
	case ModelQwenAudio31Streaming:
		return familyQwenStreaming, nil
	case ModelQwenAudio31Message:
		return familyQwenMessage, nil
	case ModelFunASRRealtime:
		return familyFun, nil
	case ModelParaformerRealtime, ModelParaformerRealtime8K:
		return familyParaformer, nil
	default:
		return 0, unsupported(fmt.Sprintf("unknown model %q", model))
	}
}
func (c Config) parameters(cfg asr.Config, format string) (map[string]any, error) {
	family, err := modelType(c.Model)
	if err != nil {
		return nil, err
	}
	native, err := nativeOptions(cfg.Options)
	if err != nil {
		return nil, err
	}
	c = c.withOverrides(native, cfg.Options)
	capability, err := SupportedAudio(c.Model)
	if err != nil {
		return nil, err
	}
	if err := capability.Validate(cfg.Format); err != nil {
		return nil, unsupportedAudio(err)
	}
	sampleRate := cfg.Format.SampleRate
	if c.MaxSentenceSilenceMs != 0 && (c.MaxSentenceSilenceMs < 200 || c.MaxSentenceSilenceMs > 6000) {
		return nil, badArgument("MaxSentenceSilenceMs must be between 200 and 6000")
	}
	if c.KeepDialect && family != familyQwenStreaming && family != familyQwenMessage {
		return nil, unsupported("KeepDialect only supports Qwen-Audio 3.1")
	}
	if c.VADModel != "" {
		if family != familyQwenStreaming && family != familyQwenMessage {
			return nil, unsupported("VADModel only supports Qwen-Audio 3.1")
		}
		if c.VADModel != "near_meeting_16k" && c.VADModel != "far_field_meeting_16k" {
			return nil, badArgument("invalid VADModel")
		}
	}
	if c.IntermediateResults && family != familyQwenMessage {
		return nil, unsupported("IntermediateResults only supports Qwen-Audio 3.1 Message")
	}
	if c.DisfluencyRemoval && family != familyQwenMessage && family != familyParaformer {
		return nil, unsupported("DisfluencyRemoval is not supported by this model")
	}
	if len(c.Vocabulary) > 0 && family != familyQwenStreaming && family != familyQwenMessage {
		return nil, unsupported("instant Vocabulary only supports Qwen-Audio 3.1")
	}
	if len(cfg.Hints) > 0 && family != familyQwenStreaming && family != familyQwenMessage {
		return nil, unsupported("ASR Hints require Qwen-Audio 3.1; use VocabularyID for other models")
	}
	params := map[string]any{"format": format, "sample_rate": sampleRate}
	if cfg.Language != "" {
		language := strings.ToLower(strings.SplitN(strings.ReplaceAll(cfg.Language, "_", "-"), "-", 2)[0])
		if language == "" {
			return nil, badArgument("invalid Language")
		}
		params["language_hints"] = []string{language}
	}
	if c.VocabularyID != "" {
		params["vocabulary_id"] = c.VocabularyID
	}
	if c.MaxSentenceSilenceMs != 0 {
		params["max_sentence_silence"] = c.MaxSentenceSilenceMs
	}
	if c.Heartbeat {
		params["heartbeat"] = true
	}
	if c.KeepDialect {
		params["keep_dialect"] = true
	}
	if c.VADModel != "" {
		params["vad_model"] = c.VADModel
	}
	if c.IntermediateResults {
		params["intermediate_result_enabled"] = true
	}
	if c.DisfluencyRemoval {
		params["disfluency_removal_enabled"] = true
	}
	if family == familyQwenStreaming || family == familyQwenMessage {
		vocabulary := make(map[string]int, len(cfg.Hints)+len(c.Vocabulary))
		for _, hint := range cfg.Hints {
			if strings.TrimSpace(hint) == "" {
				return nil, badArgument("empty ASR Hint")
			}
			vocabulary[hint] = 3
		}
		for word, weight := range c.Vocabulary {
			if strings.TrimSpace(word) == "" || !(weight >= 1 && weight <= 5 || weight == 50) {
				return nil, badArgument("Vocabulary entries require a word and weight 1..5 or 50")
			}
			vocabulary[word] = weight
		}
		if len(vocabulary) > 0 {
			params["vocabulary"] = vocabulary
		}
	}
	if err := applyAdvanced(family, c.Model, c.MaxSentenceSilenceMs, native, cfg.Options, cfg.Language, params); err != nil {
		return nil, err
	}
	return params, nil
}
func unsupported(msg string) error {
	return &speecherr.Error{Code: speecherr.CodeUnsupported, Provider: "aliyun", Operation: "configure", Err: errors.New(msg)}
}

// unsupportedAudio preserves the shared audio.ErrUnsupportedFormat sentinel,
// allowing callers to distinguish format incompatibility from other features.
func unsupportedAudio(err error) error {
	return &speecherr.Error{
		Code: speecherr.CodeUnsupported, Provider: "aliyun",
		Operation: "start", Err: err,
	}
}
