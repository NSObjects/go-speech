package tencent

import (
	"net/url"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/speecherr"
)

var (
	_ asr.CapabilityProvider = (*Client)(nil)
	_ asr.ConfigValidator     = (*Client)(nil)
)

func (c *Client) Capabilities() asr.Capabilities {
	if c == nil {
		return asr.Capabilities{}
	}
	capability := SupportedAudio(c.cfg.EngineModel)
	if c.cfg.InputSampleRate != 0 {
		capability.SampleRates = []int{c.cfg.InputSampleRate}
	}
	return asr.Capabilities{Provider: "tencent", Model: c.cfg.EngineModel, Protocol: "asr/v2", Audio: capability, Batch: true, Streaming: true}
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
