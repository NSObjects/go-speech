package tencent

import (
	"math"
	"net/url"
	"strconv"

	"github.com/NSObjects/go-speech/tts"
)

// Options uses native Tencent WSv2 units. Speed is the vendor scale
// [-2, 6], not a multiplier. Volume is on the vendor scale [-10, 10].
type Options struct {
	Speed            *float64
	Volume           *float64
	EmotionCategory  string
	EmotionIntensity *int
	SegmentRate      *int
}

func (Options) TTSProviderOptions() {}

func voiceQuery(o tts.Options) (url.Values, error) {
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
		return nil, invalid("provider options do not belong to tencent")
	}
	if o.Speed != nil || o.Instruction != "" {
		return nil, unsupported("use native Tencent speed; natural-language instruction is unsupported")
	}
	q := make(url.Values)
	if v := native.Speed; v != nil {
		if math.IsNaN(*v) || *v < -2 || *v > 6 {
			return nil, invalid("native speed must be within [-2, 6]")
		}
		q.Set("Speed", strconv.FormatFloat(*v, 'f', -1, 64))
	}
	if v := native.Volume; v != nil {
		if math.IsNaN(*v) || *v < -10 || *v > 10 {
			return nil, invalid("volume must be within [-10, 10]")
		}
		q.Set("Volume", strconv.FormatFloat(*v, 'f', -1, 64))
	}
	if native.EmotionCategory != "" {
		q.Set("EmotionCategory", native.EmotionCategory)
	}
	if v := native.EmotionIntensity; v != nil {
		if native.EmotionCategory == "" || *v < 50 || *v > 200 {
			return nil, invalid("emotion intensity requires a category and must be within [50, 200]")
		}
		q.Set("EmotionIntensity", strconv.Itoa(*v))
	}
	if v := native.SegmentRate; v != nil {
		if *v < 0 || *v > 2 {
			return nil, invalid("segment rate must be within [0, 2]")
		}
		q.Set("SegmentRate", strconv.Itoa(*v))
	}
	return q, nil
}
