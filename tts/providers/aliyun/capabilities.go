package aliyun

import (
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/NSObjects/go-speech/tts"
	"github.com/NSObjects/go-speech/capability"
	"github.com/NSObjects/go-speech/transport"
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
		Pool: transport.PoolCapabilities{Supported: true, Enabled: c.pool.Enabled(), Warmup: true, Batch: true, Streaming: true, Duplex: true},
		CompleteTextFormats: []tts.TextFormat{tts.TextPlain, tts.TextSSML},
		DuplexTextFormats:   []tts.TextFormat{tts.TextPlain},
	}
}

func (c *Client) ValidateRequest(req tts.Request) error {
	_, _, _, err := c.prepareRequest(req, false)
	return err
}

func (c *Client) ValidateSynthesize(req tts.Request) error {
	_, _, _, err := c.prepareRequest(req, true)
	return err
}

func (c *Client) ValidateDuplex(req tts.DuplexRequest) error {
	_, _, _, err := c.prepareDuplex(req)
	return err
}

// prepareRequest validates and normalizes once, returning owned native
// parameters that can be sent without reading the caller's options again.
func (c *Client) prepareRequest(req tts.Request, batch bool) (tts.Request, string, map[string]any, error) {
	if c == nil {
		return req, "", nil, invalid("nil client")
	}
	req, codec, err := validateRequest(req)
	if err != nil {
		return req, "", nil, err
	}
	params, err := voiceParameters(req.Options)
	if err != nil {
		return req, "", nil, err
	}
	if err := validateVoiceOptions(req, c.cfg.Model, params); err != nil {
		return req, "", nil, err
	}
	if enabled, _ := params["word_timestamp_enabled"].(bool); batch && enabled {
		return req, "", nil, invalid("Synthesize returns audio only; use SynthesizeStream with RecvEvent for timestamps")
	}
	return req, codec, params, nil
}

func (c *Client) prepareDuplex(cfg tts.DuplexRequest) (tts.Request, string, map[string]any, error) {
	if cfg.TextFormat == tts.TextSSML {
		return tts.Request{}, "", nil, &speecherr.Error{Code: speecherr.CodeUnsupported, Provider: "aliyun", Operation: "tts", Err: tts.ErrUnsupportedTextFormat}
	}
	return c.prepareRequest(tts.Request{Text: "validation", Voice: cfg.Voice, Format: cfg.Format, TextFormat: cfg.TextFormat, Options: cfg.Options}, false)
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
		tts.FeatureVoiceClone: {Status: capability.Unknown, Notes: "Use provider-issued voice ID; enrollment and design are available in voice/providers/aliyun."},
		tts.FeatureLatex: {Status: capability.Unsupported},
		tts.FeatureSegmentRate: {Status: capability.Unsupported},
		tts.FeatureSimilarityBoost: {Status: capability.Unsupported},
		tts.FeatureDialectBoost: {Status: capability.Unsupported},
		tts.FeatureLanguage: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.LanguageHint", Kind: capability.String, StringValues: languageHints()}}},
	}
}
