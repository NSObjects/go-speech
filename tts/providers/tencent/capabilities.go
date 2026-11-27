package tencent

import (
	"net/url"
	"strings"

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
		Provider: "tencent", Model: "", Protocol: "stream_wsv2", Audio: SupportedAudio(),
		Batch: true, Streaming: true, Duplex: true, Events: true, Reset: true,
		Features: c.features(),
		CompleteTextFormats: []tts.TextFormat{tts.TextPlain},
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
	_, _, err := c.prepareRequest(tts.Request{Text: "validation", Voice: req.Voice, Format: req.Format, TextFormat: req.TextFormat, Options: req.Options})
	return err
}

func (c *Client) validateBatchOptions(req tts.Request) error {
	q, err := voiceQuery(req.Options)
	if err != nil {
		return err
	}
	if q.Get("EnableSubtitle") == "true" {
		return invalid("Synthesize returns audio only; use SynthesizeStream with RecvEvent for timestamps")
	}
	return nil
}

func (c *Client) prepareRequest(req tts.Request) (tts.Request, string, error) {
	if c == nil {
		return req, "", invalid("nil client")
	}
	req, codec, err := validateRequest(req)
	if err != nil {
		return req, "", err
	}
	if _, err := requestVoiceQuery(req); err != nil {
		return req, "", err
	}
	return req, codec, nil
}

func requestVoiceQuery(req tts.Request) (url.Values, error) {
	voice, err := voiceQuery(req.Options)
	if err != nil {
		return nil, err
	}
	fastVoice := voice.Get("FastVoiceType")
	switch req.Voice {
	case "200000000", "200000400":
		if fastVoice == "" || strings.TrimSpace(fastVoice) != fastVoice {
			return nil, invalid("cloned VoiceType requires FastVoiceType")
		}
	default:
		if fastVoice != "" {
			return nil, invalid("FastVoiceType requires VoiceType 200000000 or 200000400")
		}
	}
	return voice, nil
}

func (c *Client) features() capability.Features {
	return capability.Features{
		tts.FeatureSpeed: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.Speed", Kind: capability.Number, Unit: "native", Range: &capability.NumericRange{Min: minSpeed, Max: maxSpeed}}}, Notes: "Not a multiplier; common Options.Speed rejected."},
		tts.FeatureVolume: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.Volume", Kind: capability.Number, Unit: "native", Range: &capability.NumericRange{Min: minVolume, Max: maxVolume}}}},
		tts.FeaturePitch: {Status: capability.Unsupported},
		tts.FeatureEmotion: {Status: capability.Unknown, Parameters: []capability.Parameter{{Field: "Options.Provider.EmotionCategory", Kind: capability.String}, {Field: "Options.Provider.EmotionIntensity", Kind: capability.Integer, Unit: "native", Range: &capability.NumericRange{Min: minEmotionIntensity, Max: maxEmotionIntensity}}}, Notes: "Voice dependent; intensity requires category."},
		tts.FeatureInstruction: {Status: capability.Unsupported},
		tts.FeatureAlignment: {Status: capability.Unknown, Parameters: []capability.Parameter{{Field: "Options.Provider.EnableSubtitle", Kind: capability.Bool}}, Notes: "Voice dependent; use RecvEvent or SynthesizeDetailed, not audio-only Synthesize."},
		tts.FeaturePronunciation: {Status: capability.Unsupported},
		tts.FeatureMarkdownFilter: {Status: capability.Unsupported},
		tts.FeatureTextNormalization: {Status: capability.Unsupported},
		tts.FeatureBitRate: {Status: capability.Unsupported},
		tts.FeatureContext: {Status: capability.Unsupported},
		tts.FeatureSeed: {Status: capability.Unsupported},
		tts.FeatureWatermark: {Status: capability.Unsupported},
		tts.FeatureSilence: {Status: capability.Unsupported},
		tts.FeatureVoiceClone: {Status: capability.Unknown, Parameters: []capability.Parameter{{Field: "Options.Provider.FastVoiceType", Kind: capability.String}}, Notes: "One-sentence cloned voice IDs require Voice 200000000 or 200000400; enrollment and task queries are available in voice/providers/tencent."},
		tts.FeatureLatex: {Status: capability.Unknown, Parameters: []capability.Parameter{{Field: "Options.Provider.EnableLatex", Kind: capability.Bool}}, Notes: "Only certain voices."},
		tts.FeatureSegmentRate: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.SegmentRate", Kind: capability.Integer, Range: &capability.NumericRange{Min: minSegmentRate, Max: maxSegmentRate}}}},
		tts.FeatureSimilarityBoost: {Status: capability.Unsupported},
		tts.FeatureDialectBoost: {Status: capability.Unsupported},
		tts.FeatureLanguage: {Status: capability.Unsupported},
	}
}
