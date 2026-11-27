package aliyun

import (
	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/capability"
)

var (
	_ asr.CapabilityProvider = (*Client)(nil)
	_ asr.ConfigValidator    = (*Client)(nil)
)

func (c *Client) Capabilities() asr.Capabilities {
	if c == nil {
		return asr.Capabilities{}
	}
	capability, _ := SupportedAudio(c.cfg.Model)
	return asr.Capabilities{Provider: "aliyun", Model: c.cfg.Model, Protocol: "dashscope-task", Audio: capability, Batch: true, Streaming: true, Features: c.features()}
}

func (c *Client) ValidateConfig(cfg asr.Config) error {
	_, _, err := c.prepare(cfg)
	return err
}

func (c *Client) prepare(cfg asr.Config) (map[string]any, map[string]any, error) {
	if c == nil {
		return nil, nil, badArgument("nil client")
	}
	format, err := mapAudioFormat(cfg.Format)
	if err != nil {
		return nil, nil, err
	}
	native, err := nativeOptions(cfg.Options)
	if err != nil {
		return nil, nil, err
	}
	params, err := c.cfg.parameters(cfg, format)
	if err != nil {
		return nil, nil, err
	}
	input, err := c.cfg.contextInput(native.Context)
	if err != nil {
		return nil, nil, err
	}
	return params, input, nil
}

func (c *Client) features() capability.Features {
	f, err := modelType(c.cfg.Model)
	if err != nil {
		return nil
	}
	para := f == familyParaformer
	return capability.Features{
		asr.FeatureEmotion: {Status: capability.FromBool(c.cfg.Model == ModelParaformerRealtime8K), Parameters: []capability.Parameter{{Field: "Options.UtteranceDetails", Kind: capability.Bool}}, Notes: "Final sentences only; disable semantic segmentation."},
		asr.FeatureSpeechRate: {Status: capability.Unsupported},
		asr.FeatureVolume: {Status: capability.Unsupported},
		asr.FeatureLanguageDetection: {Status: capability.Unsupported},
		asr.FeatureGenderDetection: {Status: capability.Unsupported},
		asr.FeatureSpeakerDiarization: {Status: capability.Unsupported},
		asr.FeatureSpeakerContext: {Status: capability.Unsupported},
		asr.FeatureWordTimestamps: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.UtteranceDetails", Kind: capability.Bool}}},
		asr.FeatureSegmentTimestamps: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.UtteranceDetails", Kind: capability.Bool}}},
		asr.FeatureContext: {Status: capability.FromBool(f.supportsContext()), Parameters: []capability.Parameter{{Field: "Options.Provider.Context", Kind: capability.List}}, Notes: "Initial context; Qwen Message also supports dynamic updates."},
		asr.FeatureDynamicContext: {Status: capability.FromBool(f.supportsDynamicContext()), Notes: "Use aliyun.ContextUpdater.UpdateContext before CloseSend."},
		asr.FeatureUsage: {Status: capability.Supported, Notes: "Provider cumulative usage when returned. Qwen 3.1 includes token counters; other models may return duration only."},
		asr.FeatureImageContext: {Status: capability.Unsupported},
		asr.FeatureHotwords: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.VocabularyID", Kind: capability.String}, {Field: "Options.Provider.Vocabulary", Kind: capability.Object}}, Notes: "Instant vocabulary and Hints require Qwen Audio; other models use VocabularyID."},
		asr.FeaturePunctuation: {Status: capability.FromBool(para), Parameters: []capability.Parameter{{Field: "Options.Punctuation", Kind: capability.Bool}}},
		asr.FeatureTextNormalization: {Status: capability.FromBool(para), Parameters: []capability.Parameter{{Field: "Options.TextNormalization", Kind: capability.Bool}}},
		asr.FeatureDisfluencyRemoval: {Status: capability.FromBool(f.supportsDisfluency()), Parameters: []capability.Parameter{{Field: "Options.DisfluencyRemoval", Kind: capability.Bool}}},
		asr.FeatureSemanticSegmentation: {Status: capability.FromBool(para), Parameters: []capability.Parameter{{Field: "Options.Provider.SemanticPunctuation", Kind: capability.Bool}}, Notes: "Conflicts with explicit silence and multi-threshold mode."},
		asr.FeatureSecondPass: {Status: capability.Unsupported},
		asr.FeatureNoiseThreshold: {Status: capability.FromBool(!para), Parameters: []capability.Parameter{{Field: "Options.Provider.SpeechNoiseThreshold", Kind: capability.Number, Unit: "native", Range: &capability.NumericRange{Min: minNoiseThreshold, Max: maxNoiseThreshold}}}},
		asr.FeatureSensitiveWords: {Status: capability.FromBool(f.supportsSensitiveWords()), Parameters: []capability.Parameter{{Field: "Options.Provider.SensitiveWords", Kind: capability.Object}}},
		asr.FeatureDialect: {Status: capability.FromBool(f.supportsQwenControls()), Parameters: []capability.Parameter{{Field: "Options.Provider.KeepDialect", Kind: capability.Bool}}},
		asr.FeatureAccelerate: {Status: capability.Unsupported},
	}
}
