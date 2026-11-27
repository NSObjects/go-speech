package volcengine

import (
	"encoding/json"

	"github.com/NSObjects/go-speech/asr"
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
	return asr.Capabilities{Provider: "volcengine", Model: "bigmodel", ResourceID: c.cfg.ResourceID, Protocol: "bigmodel_async", Audio: capability, Batch: true, Streaming: true}
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
	if err := applyCorpus(request, req["request"].(map[string]any)); err != nil {
		return nil, "", false, err
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, "", false, &speecherr.Error{Code: speecherr.CodeInternal, Provider: "volcengine", Operation: "encode", Err: err}
	}
	return payload, resultType, details, nil
}
