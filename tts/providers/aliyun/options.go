package aliyun

import (
	"math"

	"github.com/NSObjects/go-speech/tts"
)

// Options uses native CosyVoice units: volume 0–100, pitch 0.5–2,
// seed 0–65535. Model and voice support is determined by the service.
type Options struct {
	Volume *int
	Pitch  *float64
	Seed   *int
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
			if math.IsNaN(*v) || *v < 0.5 || *v > 2 {
				return nil, invalid("rate and pitch must be within [0.5, 2]")
			}
			params[key] = *v
		}
	}
	if native.Volume != nil {
		if *native.Volume < 0 || *native.Volume > 100 {
			return nil, invalid("volume must be within [0, 100]")
		}
		params["volume"] = *native.Volume
	}
	if native.Seed != nil {
		if *native.Seed < 0 || *native.Seed > 65535 {
			return nil, invalid("seed must be within [0, 65535]")
		}
		params["seed"] = *native.Seed
	}
	if o.Instruction != "" {
		params["instruction"] = o.Instruction
	}
	return params, nil
}
