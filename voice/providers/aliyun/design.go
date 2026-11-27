package aliyun

import (
	"context"
	"encoding/json"
)

// DesignRequest creates a custom voice from text instead of a recording.
// Zero SampleRate and empty ResponseFormat use the provider defaults.
type DesignRequest struct {
	TargetModel    string
	Prefix         string
	VoicePrompt    string
	PreviewText    string
	LanguageHint   string
	SampleRate     int
	ResponseFormat string
}

// PreviewAudio contains decoded bytes, in the provider's declared format.
type PreviewAudio struct {
	Data           []byte `json:"data"`
	SampleRate     int    `json:"sample_rate"`
	ResponseFormat string `json:"response_format"`
}

type DesignResult struct {
	Result
	Preview PreviewAudio
}

func (r DesignRequest) wire() (map[string]any, map[string]any, error) {
	if !supportsDesign(r.TargetModel) {
		return nil, nil, unsupported("target model is not documented for voice design")
	}
	if !validPrefix(r.Prefix) || !validText(r.VoicePrompt, 1, 500) || !validText(r.PreviewText, 15, 200) {
		return nil, nil, invalid("invalid prefix, voice prompt or preview text")
	}
	in := map[string]any{
		"action": "create_voice", "target_model": r.TargetModel, "prefix": r.Prefix,
		"voice_prompt": r.VoicePrompt, "preview_text": r.PreviewText,
	}
	if r.LanguageHint != "" {
		if r.LanguageHint != "zh" && r.LanguageHint != "en" {
			return nil, nil, invalid("design language hint must be zh or en")
		}
		in["language_hints"] = []string{r.LanguageHint}
	}
	params := make(map[string]any)
	switch r.SampleRate {
	case 0:
	case 16000, 24000, 48000:
		params["sample_rate"] = r.SampleRate
	default:
		return nil, nil, invalid("invalid preview sample rate")
	}
	switch r.ResponseFormat {
	case "":
	case "pcm", "wav", "mp3":
		params["response_format"] = r.ResponseFormat
	default:
		return nil, nil, invalid("invalid preview format")
	}
	return in, params, nil
}

func (c *Client) Design(ctx context.Context, req DesignRequest) (DesignResult, error) {
	in, params, err := req.wire()
	if err != nil {
		return DesignResult{}, err
	}
	var out struct {
		Voice
		Preview json.RawMessage `json:"preview_audio"`
	}
	meta, err := c.call(ctx, in, params, &out)
	if err != nil {
		return DesignResult{}, err
	}
	if !validID(out.VoiceID) {
		return DesignResult{}, protocol("create_voice", "missing designed voice ID")
	}
	// Creation already occurred if the ID is present. Preserve it even
	// if the service omitted or returned an invalid preview.
	result := DesignResult{Result: Result{Metadata: meta, Voice: out.Voice}}
	if out.TargetModel == "" {
		result.TargetModel = req.TargetModel
	} else if out.TargetModel != req.TargetModel {
		return result, protocol("create_voice", "target model mismatch")
	}
	if err := json.Unmarshal(out.Preview, &result.Preview); err != nil {
		return result, protocol("create_voice", "invalid preview audio encoding")
	}
	p := result.Preview
	if len(p.Data) == 0 || (p.SampleRate != 16000 && p.SampleRate != 24000 && p.SampleRate != 48000) ||
		(p.ResponseFormat != "pcm" && p.ResponseFormat != "wav" && p.ResponseFormat != "mp3") {
		return result, protocol("create_voice", "invalid preview audio")
	}
	return result, nil
}
