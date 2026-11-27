package aliyun

import (
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/NSObjects/go-speech/tts"
	"github.com/NSObjects/go-speech/capability"
)

var (
	_ tts.CapabilityProvider = (*Client)(nil)
	_ tts.RequestValidator   = (*Client)(nil)
)

func (c *Client) Capabilities() tts.Capabilities {
	if c == nil {
		return tts.Capabilities{}
	}
	return tts.Capabilities{
		Provider: "aliyun", Model: c.cfg.Model, Protocol: "dashscope-task", Audio: SupportedAudio(),
		Batch: true, Streaming: true, Duplex: true, Events: true, Cancel: true,
		Features: c.features(),
		CompleteTextFormats: []tts.TextFormat{tts.TextPlain, tts.TextSSML},
		DuplexTextFormats:   []tts.TextFormat{tts.TextPlain},
	}
}

func (c *Client) ValidateRequest(req tts.Request) error {
	_, _, err := c.prepareRequest(req)
	return err
}

func (c *Client) ValidateSynthesize(req tts.Request) error {
	if err := c.validateBatchOptions(req); err != nil {
		return err
	}
	return c.ValidateRequest(req)
}

func (c *Client) ValidateDuplex(req tts.DuplexRequest) error {
	_, _, err := c.prepareDuplex(req)
	return err
}

func (c *Client) validateBatchOptions(req tts.Request) error {
	params, err := voiceParameters(req.Options)
	if err != nil {
		return err
	}
	if enabled, _ := params["word_timestamp_enabled"].(bool); enabled {
		return invalid("Synthesize returns audio only; use SynthesizeStream with RecvEvent for timestamps")
	}
	return nil
}

func (c *Client) prepareRequest(req tts.Request) (tts.Request, string, error) {
	if c == nil {
		return req, "", invalid("nil client")
	}
	req, encoding, err := validateRequest(req)
	if err != nil {
		return req, "", err
	}
	if err := validateVoiceOptions(req, c.cfg.Model); err != nil {
		return req, "", err
	}
	return req, encoding, nil
}

func (c *Client) prepareDuplex(cfg tts.DuplexRequest) (tts.Request, string, error) {
	if cfg.TextFormat == tts.TextSSML {
		return tts.Request{}, "", &speecherr.Error{Code: speecherr.CodeUnsupported, Provider: "aliyun", Operation: "tts", Err: tts.ErrUnsupportedTextFormat}
	}
	return c.prepareRequest(tts.Request{Text: "validation", Voice: cfg.Voice, Format: cfg.Format, TextFormat: cfg.TextFormat, Options: cfg.Options})
}

func (c *Client) features() capability.Features {
	markdown := capability.Unknown
	if !supportsMarkdownFilter(c.cfg.Model) {
		markdown = capability.Unsupported
	}
	return capability.Features{
		tts.FeatureSpeed: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Speed", Kind: capability.Number, Unit: "multiplier", Range: &capability.NumericRange{Min: minMultiplier, Max: maxMultiplier}}}},
		tts.FeatureVolume: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.Volume", Kind: capability.Integer, Unit: "native", Range: &capability.NumericRange{Min: minVolume, Max: maxVolume}}}},
		tts.FeaturePitch: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.Pitch", Kind: capability.Number, Unit: "multiplier", Range: &capability.NumericRange{Min: minMultiplier, Max: maxMultiplier}}}},
		tts.FeatureEmotion: {Status: capability.Unknown, Parameters: []capability.Parameter{{Field: "Options.Instruction", Kind: capability.String}}, Notes: "Model/voice dependent."},
		tts.FeatureInstruction: {Status: capability.Unknown, Parameters: []capability.Parameter{{Field: "Options.Instruction", Kind: capability.String}}, Notes: "Model/voice dependent."},
		tts.FeatureAlignment: {Status: capability.Unknown, Parameters: []capability.Parameter{{Field: "Options.Provider.WordTimestampEnabled", Kind: capability.Bool}}, Notes: "Use RecvEvent or SynthesizeDetailed; not audio-only Synthesize."},
		tts.FeaturePronunciation: {Status: capability.Unsupported},
		tts.FeatureMarkdownFilter: {Status: markdown, Parameters: []capability.Parameter{{Field: "Options.Provider.EnableMarkdownFilter", Kind: capability.Bool}}, Notes: "v2 rejected by SDK; other models need service confirmation."},
		tts.FeatureTextNormalization: {Status: capability.Unsupported},
		tts.FeatureBitRate: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.BitRateKbps", Kind: capability.Integer, Unit: "kbps", Range: &capability.NumericRange{Min: minBitRate, Max: maxBitRate}}}, Notes: "MP3/Ogg Opus only."},
		tts.FeatureContext: {Status: capability.Unsupported},
		tts.FeatureSeed: {Status: capability.Unknown, Parameters: []capability.Parameter{{Field: "Options.Provider.Seed", Kind: capability.Integer, Range: &capability.NumericRange{Min: minSeed, Max: maxSeed}}}},
		tts.FeatureWatermark: {Status: capability.FromBool(supportsAIGCTag(c.cfg.Model)), Parameters: []capability.Parameter{{Field: "Options.Provider.EnableAIGCTag", Kind: capability.Bool}, {Field: "Options.Provider.AIGCPropagator", Kind: capability.String}, {Field: "Options.Provider.AIGCPropagateID", Kind: capability.String}}, Notes: "Not PCM."},
		tts.FeatureSilence: {Status: capability.Unsupported},
		tts.FeatureVoiceClone: {Status: capability.Unknown, Notes: "Use provider-issued voice ID; training API not implemented."},
		tts.FeatureLatex: {Status: capability.Unsupported},
		tts.FeatureSegmentRate: {Status: capability.Unsupported},
		tts.FeatureSimilarityBoost: {Status: capability.Unsupported},
		tts.FeatureDialectBoost: {Status: capability.Unsupported},
		tts.FeatureLanguage: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.LanguageHint", Kind: capability.String}}},
	}
}
