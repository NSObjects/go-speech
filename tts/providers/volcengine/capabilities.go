package volcengine

import (
	"encoding/json"

	"github.com/NSObjects/go-speech/audio"
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
		Provider: "volcengine", Model: c.cfg.ModelID, Protocol: "seed-tts-http-jsonl/ws", Audio: SupportedAudio(),
		Batch: true, Streaming: true, Duplex: true, Events: true, Flush: true,
		Features: c.features(),
		Pool: transport.PoolCapabilities{Supported: true, Enabled: c.pool.Enabled(), Warmup: true, VoiceSpecific: true, Duplex: true},
		CompleteTextFormats: []tts.TextFormat{tts.TextPlain, tts.TextSSML, tts.TextInlineTag},
		DuplexTextFormats:   []tts.TextFormat{tts.TextPlain, tts.TextInlineTag},
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
	native, err := nativeOptions(req.Options)
	if err != nil {
		return err
	}
	if native.EnableSubtitle != nil && *native.EnableSubtitle {
		return invalid("Synthesize returns audio only; use SynthesizeStream with RecvEvent for subtitles")
	}
	return nil
}

func (c *Client) prepareRequest(req tts.Request) (audio.Format, []byte, error) {
	if c == nil || !validText(req.Text) {
		return audio.Format{}, nil, invalid("client and 1–10000 UTF-8 characters required")
	}
	format, output, err := validateOutput(req.Voice, req.Format)
	if err != nil {
		return audio.Format{}, nil, err
	}
	native, err := nativeOptions(req.Options)
	if err != nil {
		return audio.Format{}, nil, err
	}
	voice, err := buildVoiceSettings(req.Options, native)
	if err != nil {
		return audio.Format{}, nil, err
	}
	extra, err := advanced(native, &output, false)
	if err != nil {
		return audio.Format{}, nil, err
	}
	language := c.cfg.LanguageCode
	if native.LanguageCode != "" {
		language = native.LanguageCode
	}
	body := synthesisRequest{
		VoiceSettings: voice,
		ModelID: c.cfg.ModelID, LanguageCode: language, OutputFormat: output,
		SessionID: native.SessionID, SilenceDurationMS: native.SilenceDurationMS,
		advancedRequest: extra,
	}
	switch req.TextFormat {
	case "", tts.TextPlain, tts.TextInlineTag:
		body.Text = req.Text
	case tts.TextSSML:
		body.SSML = req.Text
	default:
		return audio.Format{}, nil, invalid("unknown text format")
	}
	data, err := json.Marshal(body)
	if err != nil {
		return audio.Format{}, nil, err
	}
	return format, data, nil
}

func (c *Client) prepareDuplex(req tts.DuplexRequest) (audio.Format, clientEvent, error) {
	if c == nil {
		return audio.Format{}, clientEvent{}, invalid("nil client")
	}
	switch req.TextFormat {
	case "", tts.TextPlain, tts.TextInlineTag:
	case tts.TextSSML:
		return audio.Format{}, clientEvent{}, failure(speecherr.CodeUnsupported, "validate", tts.ErrUnsupportedTextFormat)
	default:
		return audio.Format{}, clientEvent{}, invalid("unknown text format")
	}
	format, output, err := validateOutput(req.Voice, req.Format)
	if err != nil {
		return audio.Format{}, clientEvent{}, err
	}
	native, err := nativeOptions(req.Options)
	if err != nil {
		return audio.Format{}, clientEvent{}, err
	}
	voice, err := buildVoiceSettings(req.Options, native)
	if err != nil {
		return audio.Format{}, clientEvent{}, err
	}
	extra, err := advanced(native, &output, true)
	if err != nil {
		return audio.Format{}, clientEvent{}, err
	}
	language := c.cfg.LanguageCode
	if native.LanguageCode != "" {
		language = native.LanguageCode
	}
	return format, clientEvent{Event: "init", SessionID: native.SessionID, ModelID: c.cfg.ModelID, LanguageCode: language, OutputFormat: &output, VoiceSettings: voice, advancedRequest: extra}, nil
}

func (c *Client) features() capability.Features {
	return capability.Features{
		tts.FeatureSpeed: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Speed", Kind: capability.Number, Unit: "multiplier", Range: &capability.NumericRange{Min: minMultiplier, Max: maxMultiplier}}}},
		tts.FeatureVolume: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.Volume", Kind: capability.Number, Unit: "multiplier", Range: &capability.NumericRange{Min: minMultiplier, Max: maxMultiplier}}}},
		tts.FeaturePitch: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.Pitch", Kind: capability.Integer, Unit: "semitones", Range: &capability.NumericRange{Min: minPitch, Max: maxPitch}}}},
		tts.FeatureEmotion: {Status: capability.Unknown, Parameters: []capability.Parameter{{Field: "Options.Instruction", Kind: capability.String}}, Notes: "Instruction or native inline tags; voice and text mode dependent."},
		tts.FeatureInstruction: {Status: capability.Unknown, Parameters: []capability.Parameter{{Field: "Options.Instruction", Kind: capability.String}, {Field: "Options.Provider.Instructions", Kind: capability.List}}, Notes: "Do not combine; voice dependent."},
		tts.FeatureAlignment: {Status: capability.Unknown, Parameters: []capability.Parameter{{Field: "Options.Provider.EnableSubtitle", Kind: capability.Bool}}, Notes: "Use RecvEvent or SynthesizeDetailed; not audio-only Synthesize."},
		tts.FeaturePronunciation: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.PronunciationTone", Kind: capability.List}}},
		tts.FeatureMarkdownFilter: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.EnableMarkdownFilter", Kind: capability.Bool}}},
		tts.FeatureTextNormalization: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.TextNormalization", Kind: capability.Bool}}},
		tts.FeatureBitRate: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.BitRate", Kind: capability.Integer, Unit: "bps", IntValues: []int{standardBitRate, highBitRate}}}, Notes: "MP3/Ogg Opus only; 8k MP3 requires 64000."},
		tts.FeatureContext: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.SessionID", Kind: capability.String}}, Notes: "Serial synthesis context, not a persistent SDK session."},
		tts.FeatureSeed: {Status: capability.Unsupported},
		tts.FeatureWatermark: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.Watermark", Kind: capability.Object}}},
		tts.FeatureSilence: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.SilenceDurationMS", Kind: capability.Number, Unit: "ms", Range: &capability.NumericRange{Min: minSilenceMS, Max: maxSilenceMS}}}, Notes: "Complete text only."},
		tts.FeatureVoiceClone: {Status: capability.Unknown, Notes: "Use a provider-issued voice ID; enrollment and design are available in voice/providers/volcengine."},
		tts.FeatureLatex: {Status: capability.Unsupported},
		tts.FeatureSegmentRate: {Status: capability.Unsupported},
		tts.FeatureSimilarityBoost: {Status: capability.Unknown, Parameters: []capability.Parameter{{Field: "Options.Provider.SimilarityBoost", Kind: capability.Bool}}},
		tts.FeatureDialectBoost: {Status: capability.Unknown, Parameters: []capability.Parameter{{Field: "Options.Provider.DialectBoost", Kind: capability.Bool}}, Notes: "Requires instruction."},
		tts.FeatureLanguage: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.LanguageCode", Kind: capability.String}}},
	}
}
