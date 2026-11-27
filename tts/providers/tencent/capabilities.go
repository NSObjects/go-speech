package tencent

import (
	"net/url"
	"strings"

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
		Provider: "tencent", Model: "", Protocol: "stream_wsv2", Audio: SupportedAudio(),
		Batch: true, Streaming: true, Duplex: true, Events: true, Reset: true,
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
