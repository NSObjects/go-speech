package aliyun

import (
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
		Provider: "aliyun", Model: c.cfg.Model, Protocol: "dashscope-task", Audio: SupportedAudio(),
		Batch: true, Streaming: true, Duplex: true, Events: true, Cancel: true,
		CompleteTextFormats: []tts.TextFormat{tts.TextPlain, tts.TextSSML},
		DuplexTextFormats: []tts.TextFormat{tts.TextPlain},
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
