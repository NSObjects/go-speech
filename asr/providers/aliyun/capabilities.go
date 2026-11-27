package aliyun

import (
	"github.com/NSObjects/go-speech/asr"
)

var (
	_ asr.CapabilityProvider = (*Client)(nil)
	_ asr.ConfigValidator     = (*Client)(nil)
)

func (c *Client) Capabilities() asr.Capabilities {
	if c == nil {
		return asr.Capabilities{}
	}
	capability, _ := SupportedAudio(c.cfg.Model)
	return asr.Capabilities{Provider: "aliyun", Model: c.cfg.Model, Protocol: "dashscope-task", Audio: capability, Batch: true, Streaming: true}
}

func (c *Client) ValidateConfig(cfg asr.Config) error {
	_, _, err := c.prepare(cfg)
	return err
}

func (c *Client) prepare(cfg asr.Config) (map[string]any, map[string]any, error) {
	if c == nil {
		return nil, nil, badArgument("nil client")
	}
	format, err := mapAudioFormat(cfg.Format)
	if err != nil {
		return nil, nil, err
	}
	native, err := nativeOptions(cfg.Options)
	if err != nil {
		return nil, nil, err
	}
	params, err := c.cfg.parameters(cfg, format)
	if err != nil {
		return nil, nil, err
	}
	input, err := c.cfg.contextInput(native.Context)
	if err != nil {
		return nil, nil, err
	}
	return params, input, nil
}
