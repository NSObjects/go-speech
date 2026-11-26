package volcengine

import (
	"math"

	"github.com/NSObjects/go-speech/tts"
)

// Options exposes Seed-TTS 3.0 native voice controls.
// Volume is a multiplier; Pitch is a semitone offset.
type Options struct {
	Volume          *float64
	Pitch           *int
	SimilarityBoost *bool
	DialectBoost    *bool
}

func (Options) TTSProviderOptions() {}

type voiceSettings struct {
	Speed           *float64 `json:"speed,omitempty"`
	Volume          *float64 `json:"volume,omitempty"`
	Pitch           *int     `json:"pitch,omitempty"`
	Instructions    []string `json:"instructions,omitempty"`
	SimilarityBoost *bool    `json:"similarity_boost,omitempty"`
	DialectBoost    *bool    `json:"dialect_boost,omitempty"`
}

func settings(o tts.Options) (*voiceSettings, error) {
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
		return nil, invalid("provider options do not belong to volcengine")
	}
	for _, v := range []*float64{o.Speed, native.Volume} {
		if v != nil && (math.IsNaN(*v) || *v < 0.5 || *v > 2) {
			return nil, invalid("speed and volume must be within [0.5, 2]")
		}
	}
	if native.Pitch != nil && (*native.Pitch < -12 || *native.Pitch > 12) {
		return nil, invalid("pitch must be within [-12, 12]")
	}
	if native.DialectBoost != nil && *native.DialectBoost && o.Instruction == "" {
		return nil, invalid("dialect boost requires an instruction")
	}
	if o.Speed == nil && o.Instruction == "" && native == (Options{}) {
		return nil, nil
	}
	s := &voiceSettings{
		Speed: o.Speed, Volume: native.Volume, Pitch: native.Pitch,
		SimilarityBoost: native.SimilarityBoost, DialectBoost: native.DialectBoost,
	}
	if o.Instruction != "" {
		s.Instructions = []string{o.Instruction}
	}
	return s, nil
}
