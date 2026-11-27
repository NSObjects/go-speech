package volcengine

import (
	"encoding/json"

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
	capability := SupportedAudio()
	return asr.Capabilities{Provider: "volcengine", Model: "bigmodel", ResourceID: c.cfg.ResourceID, Protocol: "bigmodel_async", Audio: capability, Batch: true, Streaming: true, Features: c.features()}
}

func (c *Client) ValidateConfig(cfg asr.Config) error {
	_, _, _, err := c.prepare(cfg)
	return err
}

func (c *Client) prepare(request asr.Config) ([]byte, string, bool, error) {
	if c == nil {
		return nil, "", false, argumentError("nil client")
	}
	format, codec, err := modelAudio(request.Format)
	if err != nil {
		return nil, "", false, err
	}
	if request.Language != "" {
		return nil, "", false, unsupportedError("Language selection is not supported on bigmodel_async")
	}
	req := map[string]any{
		"user": map[string]any{"uid": c.cfg.UID},
		"audio": map[string]any{
			"format": format, "codec": codec, "rate": request.Format.SampleRate,
			"bits": 16, "channel": request.Format.Channels,
		},
		"request": map[string]any{
			"model_name": "bigmodel", "enable_itn": true, "enable_punc": true,
			"show_utterances": true, "enable_nonstream": c.cfg.EnableNonStream,
			"result_type": c.cfg.ResultType,
		},
	}
	resultType, details, err := c.cfg.applyOptions(request.Options, req["request"].(map[string]any))
	if err != nil {
		return nil, "", false, err
	}
	if err := applyCorpus(request, req["request"].(map[string]any), c.cfg.ResourceID); err != nil {
		return nil, "", false, err
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, "", false, &speecherr.Error{Code: speecherr.CodeInternal, Provider: "volcengine", Operation: "encode", Err: err}
	}
	return payload, resultType, details, nil
}

func (c *Client) features() capability.Features {
	imageStatus := capability.Unknown
	switch {
	case isSeedResource(c.cfg.ResourceID):
		imageStatus = capability.Supported
	case isLegacyResource(c.cfg.ResourceID):
		imageStatus = capability.Unsupported
	}
	return capability.Features{
		asr.FeatureEmotion: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.EnableEmotionDetection", Kind: capability.Bool}}, Notes: "Native emotion label; can enable server VAD segmentation."},
		asr.FeatureSpeechRate: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.ShowSpeechRate", Kind: capability.Bool}}, Notes: "Provider tokens per second."},
		asr.FeatureVolume: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.ShowVolume", Kind: capability.Bool}}, Notes: "VolumeDB is provider-reported decibels."},
		asr.FeatureLanguageDetection: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.EnableLID", Kind: capability.Bool}}, Notes: "Native language label in utterance details."},
		asr.FeatureGenderDetection: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.EnableGenderDetection", Kind: capability.Bool}}, Notes: "Provider estimate, not verified identity."},
		asr.FeatureSpeakerDiarization: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.EnableSpeakerInfo", Kind: capability.Bool}}, Notes: "Requires EnableNonStream. Numeric speaker IDs mapped; nonnumeric labels retained in native metadata."},
		asr.FeatureSpeakerContext: {Status: capability.Unsupported},
		asr.FeatureWordTimestamps: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.UtteranceDetails", Kind: capability.Bool}}},
		asr.FeatureSegmentTimestamps: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.UtteranceDetails", Kind: capability.Bool}}},
		asr.FeatureContext: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.Corpus", Kind: capability.Object}}},
		asr.FeatureImageContext: {Status: imageStatus, Parameters: []capability.Parameter{{Field: "Options.Provider.Corpus.Context", Kind: capability.List}}, Notes: "One image URL; requires Seed-ASR 2.0 and EnableNonStream."},
		asr.FeatureHotwords: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Hints", Kind: capability.List}, {Field: "Options.Provider.Corpus", Kind: capability.Object}}},
		asr.FeaturePunctuation: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Punctuation", Kind: capability.Bool}}},
		asr.FeatureTextNormalization: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.TextNormalization", Kind: capability.Bool}}},
		asr.FeatureDisfluencyRemoval: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.DisfluencyRemoval", Kind: capability.Bool}}},
		asr.FeatureSemanticSegmentation: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.VADSegmentDuration", Kind: capability.Integer, Unit: "ms"}}, Notes: "Positive whole milliseconds; conflicts with EndWindowSize; Go field is time.Duration."},
		asr.FeatureSecondPass: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.EnableNonStream", Kind: capability.Bool}}},
		asr.FeatureNoiseThreshold: {Status: capability.Unsupported},
		asr.FeatureSensitiveWords: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.SensitiveWords", Kind: capability.Object}}},
		asr.FeatureDialect: {Status: capability.Unsupported},
		asr.FeatureAccelerate: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.EnableAccelerateText", Kind: capability.Bool}, {Field: "Options.Provider.AccelerateScore", Kind: capability.Integer, Range: &capability.NumericRange{Min: minAccelerateScore, Max: maxAccelerateScore}}}, Notes: "Score requires EnableAccelerateText."},
	}
}
