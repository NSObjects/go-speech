package aliyun

import (
	"math"
	"slices"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/tts"
)

const (
	minMultiplier = 0.5
	maxMultiplier = 2
	minVolume = 0
	maxVolume = 100
	minSeed = 0
	maxSeed = 65535
	minBitRate = 6
	maxBitRate = 510
)

// Options uses native CosyVoice units: volume 0–100, pitch 0.5–2,
// seed 0–65535. Model and voice support is determined by the service.
type Options struct {
	Volume *int
	Pitch  *float64
	Seed   *int

	// WordTimestampEnabled requests provider word timings; use RecvEvent.
	WordTimestampEnabled *bool

	// BitRateKbps is valid for MP3 and Ogg Opus, 6–510 kbps.
	BitRateKbps int
	// LanguageHint is one native language code; the wire field is an array.
	LanguageHint         string
	EnableMarkdownFilter *bool
	EnableAIGCTag        *bool
	AIGCPropagator       string
	AIGCPropagateID      string
}

func (Options) TTSProviderOptions() {}

func voiceParameters(o tts.Options) (map[string]any, error) {
	var native Options
	switch p := o.Provider.(type) {
	case nil:
	case Options:
		native = p
	case *Options:
		if p == nil {
			return nil, invalid("nil provider options")
		}
		native = *p
	default:
		return nil, invalid("provider options do not belong to aliyun")
	}
	params := make(map[string]any)
	for key, v := range map[string]*float64{"rate": o.Speed, "pitch": native.Pitch} {
		if v != nil {
			if math.IsNaN(*v) || *v < minMultiplier || *v > maxMultiplier {
				return nil, invalid("rate and pitch must be within [0.5, 2]")
			}
			params[key] = *v
		}
	}
	if native.Volume != nil {
		if *native.Volume < minVolume || *native.Volume > maxVolume {
			return nil, invalid("volume must be within [0, 100]")
		}
		params["volume"] = *native.Volume
	}
	if native.Seed != nil {
		if *native.Seed < minSeed || *native.Seed > maxSeed {
			return nil, invalid("seed must be within [0, 65535]")
		}
		params["seed"] = *native.Seed
	}
	if o.Instruction != "" {
		params["instruction"] = o.Instruction
	}
	if native.WordTimestampEnabled != nil {
		params["word_timestamp_enabled"] = *native.WordTimestampEnabled
	}
	if native.BitRateKbps != 0 {
		if native.BitRateKbps < minBitRate || native.BitRateKbps > maxBitRate {
			return nil, invalid("bit rate must be within [6, 510] kbps")
		}
		params["bit_rate"] = native.BitRateKbps
	}
	if native.LanguageHint != "" {
		if !slices.Contains(languageHints(), native.LanguageHint) {
			return nil, invalid("unsupported language hint")
		}
		params["language_hints"] = []string{native.LanguageHint}
	}
	if native.EnableMarkdownFilter != nil {
		params["enable_markdown_filter"] = *native.EnableMarkdownFilter
	}
	if native.EnableAIGCTag != nil {
		params["enable_aigc_tag"] = *native.EnableAIGCTag
	}
	if native.AIGCPropagator != "" || native.AIGCPropagateID != "" {
		if native.EnableAIGCTag == nil || !*native.EnableAIGCTag {
			return nil, invalid("AIGC metadata requires EnableAIGCTag")
		}
	}
	if native.AIGCPropagator != "" {
		params["aigc_propagator"] = native.AIGCPropagator
	}
	if native.AIGCPropagateID != "" {
		params["aigc_propagate_id"] = native.AIGCPropagateID
	}
	return params, nil
}

func validateVoiceOptions(req tts.Request, model string) error {
	params, err := voiceParameters(req.Options)
	if err != nil {
		return err
	}
	if _, ok := params["bit_rate"]; ok && req.Format.Encoding != audio.EncodingMP3 && req.Format.Encoding != audio.EncodingOpus {
		return invalid("bit rate requires MP3 or Ogg Opus")
	}
	if enabled, _ := params["enable_markdown_filter"].(bool); enabled && !supportsMarkdownFilter(model) {
		return unsupported("Markdown filtering is not supported by cosyvoice-v2")
	}
	if enabled, _ := params["enable_aigc_tag"].(bool); enabled {
		if req.Format.Encoding == audio.EncodingPCM16LE {
			return invalid("AIGC tagging requires WAV, MP3 or Ogg Opus")
		}
		if !supportsAIGCTag(model) {
			return unsupported("AIGC tagging is not documented for the selected model")
		}
	}
	return nil
}

func supportsAIGCTag(model string) bool {
	switch model {
	case "cosyvoice-v3-flash", "cosyvoice-v3-plus", "cosyvoice-v2":
		return true
	default:
		return false
	}
}

func supportsMarkdownFilter(model string) bool {
	return model != "cosyvoice-v2"
}

func languageHints() []string {
	return []string{"zh", "en", "fr", "de", "ja", "ko", "ru", "pt", "th", "id", "vi", "es", "it", "ms", "fil", "ar"}
}
