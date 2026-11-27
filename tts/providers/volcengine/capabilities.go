package volcengine

import (
	"encoding/json"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/NSObjects/go-speech/tts"
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
