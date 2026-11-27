package tencent

import (
	"net/url"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/capability"
	"github.com/NSObjects/go-speech/speecherr"
)

var (
	_ asr.CapabilityProvider = (*Client)(nil)
	_ asr.ConfigValidator    = (*Client)(nil)
)

func (c *Client) Capabilities() asr.Capabilities {
	if c == nil {
		return asr.Capabilities{}
	}
	capability := SupportedAudio(c.cfg.EngineModel)
	if c.cfg.InputSampleRate != 0 {
		capability.SampleRates = []int{c.cfg.InputSampleRate}
	}
	return asr.Capabilities{Provider: "tencent", Model: c.cfg.EngineModel, Protocol: "asr/v2", Audio: capability, Batch: true, Streaming: true, Features: c.features()}
}

func (c *Client) ValidateConfig(cfg asr.Config) error {
	_, _, _, _, err := c.prepare(cfg)
	return err
}

func (c *Client) prepare(req asr.Config) (Client, Options, url.Values, string, error) {
	if c == nil {
		return Client{}, Options{}, nil, "", argumentError("nil client")
	}
	native, err := nativeOptions(req.Options)
	if err != nil {
		return Client{}, Options{}, nil, "", err
	}
	effective := *c
	effective.cfg = c.cfg.withOverrides(native)
	if err := validateSegmentation(effective.cfg); err != nil {
		return Client{}, Options{}, nil, "", err
	}
	extra, hotwords, err := effective.cfg.requestOptions(req, native)
	if err != nil {
		return Client{}, Options{}, nil, "", err
	}
	if err := req.Format.Validate(); err != nil {
		return Client{}, Options{}, nil, "", &speecherr.Error{Code: speecherr.CodeInvalidArgument, Provider: "tencent", Operation: "start", Err: err}
	}
	capability := SupportedAudio(c.cfg.EngineModel)
	if c.cfg.InputSampleRate != 0 {
		capability.SampleRates = []int{c.cfg.InputSampleRate}
	}
	if err := capability.Validate(req.Format); err != nil {
		return Client{}, Options{}, nil, "", &speecherr.Error{Code: speecherr.CodeUnsupported, Provider: "tencent", Operation: "start", Err: err}
	}
	if req.Language != "" {
		return Client{}, Options{}, nil, "", unsupportedError("language is selected by EngineModel; per-request Language is not supported")
	}
	return effective, native, extra, hotwords, nil
}

func (c *Client) features() capability.Features {
	engine := c.cfg.EngineModel
	wordModes := wordInfoModes(engine)
	emotion := capability.Unsupported
	if supportsEmotion(engine) {
		emotion = capability.Unknown
	}
	return capability.Features{
		asr.FeatureEmotion: {Status: emotion, Parameters: []capability.Parameter{{Field: "Options.Provider.EmotionRecognition", Kind: capability.Integer, IntValues: []int{0, 1, 2}}}, Notes: "Billable; requires NeedVAD. Response schema is undocumented; raw result preserved."},
		asr.FeatureSpeechRate: {Status: capability.Unsupported},
		asr.FeatureVolume: {Status: capability.Unsupported},
		asr.FeatureLanguageDetection: {Status: capability.Unsupported},
		asr.FeatureGenderDetection: {Status: capability.Unsupported},
		asr.FeatureSpeakerDiarization: {Status: capability.FromBool(isSpeakerEngine(engine)), Notes: "Speaker ID returned by dedicated 2.0 engine."},
		asr.FeatureSpeakerContext: {Status: capability.FromBool(isSpeakerEngine(engine)), Parameters: []capability.Parameter{{Field: "Options.Provider.EnableSpeakerContext", Kind: capability.Bool}, {Field: "Options.Provider.SpeakerContextID", Kind: capability.String}}},
		asr.FeatureWordTimestamps: {Status: capability.FromBool(len(wordModes) != 0), Parameters: []capability.Parameter{{Field: "Options.Provider.WordInfo", Kind: capability.Integer, IntValues: wordModes}}},
		asr.FeatureSegmentTimestamps: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.UtteranceDetails", Kind: capability.Bool}}},
		asr.FeatureContext: {Status: capability.Unsupported},
		asr.FeatureImageContext: {Status: capability.Unsupported},
		asr.FeatureHotwords: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.HotwordID", Kind: capability.String}, {Field: "Options.Provider.Hotwords", Kind: capability.List}}, Notes: "HotwordID and temporary hotwords are mutually exclusive."},
		asr.FeaturePunctuation: {Status: capability.Unsupported, Parameters: []capability.Parameter{{Field: "Options.Provider.FilterPunctuation", Kind: capability.Bool}}, Notes: "Common punctuation control is unsupported; legacy native filter only removes sentence-final full stop."},
		asr.FeatureTextNormalization: {Status: capability.Unsupported, Parameters: []capability.Parameter{{Field: "Options.Provider.ConvertNumMode", Kind: capability.Integer, IntValues: []int{0, 1, 3}}}, Notes: "Common ITN is unsupported; native numeric conversion is narrower."},
		asr.FeatureDisfluencyRemoval: {Status: capability.FromBool(isMandarinEngine(engine)), Parameters: []capability.Parameter{{Field: "Options.DisfluencyRemoval", Kind: capability.Bool}, {Field: "Options.Provider.FilterModal", Kind: capability.Integer, IntValues: []int{0, 1, 2}}}, Notes: "Do not combine the two controls."},
		asr.FeatureSemanticSegmentation: {Status: capability.Unsupported},
		asr.FeatureSecondPass: {Status: capability.Unsupported},
		asr.FeatureNoiseThreshold: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.NoiseThreshold", Kind: capability.Number, Unit: "native", Range: &capability.NumericRange{Min: minNoiseThreshold, Max: maxNoiseThreshold}}}},
		asr.FeatureSensitiveWords: {Status: capability.FromBool(isMandarinEngine(engine)), Parameters: []capability.Parameter{{Field: "Options.Provider.FilterDirty", Kind: capability.Integer, IntValues: []int{0, 1, 2}}}},
		asr.FeatureDialect: {Status: capability.Unsupported},
		asr.FeatureAccelerate: {Status: capability.Unsupported},
	}
}
