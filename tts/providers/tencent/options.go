package tencent

import (
	"errors"
	"math"
	"net/url"
	"strconv"

	"github.com/NSObjects/go-speech/speecherr"
	"github.com/NSObjects/go-speech/tts"
)

const (
	minSpeed = -2
	maxSpeed = 6
	minVolume = -10
	maxVolume = 10
	minEmotionIntensity = 50
	maxEmotionIntensity = 200
	minSegmentRate = 0
	maxSegmentRate = 2
)

// Options uses native Tencent WSv2 units. Speed is the vendor scale
// [-2, 6], not a multiplier. Volume is on the vendor scale [-10, 10].
type Options struct {
	Speed            *float64
	Volume           *float64
	EmotionCategory  string
	EmotionIntensity *int
	SegmentRate      *int
	// FastVoiceType is the native one-sentence voice clone ID. Voice must
	// select the corresponding 200000000 or 200000400 VoiceType.
	FastVoiceType string
	EnableLatex   *bool
	// EnableSubtitle requests provider word timings; use RecvEvent.
	EnableSubtitle *bool
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
		return nil, &speecherr.Error{Code: speecherr.CodeUnsupported, Provider: "tencent", Operation: "tts", Err: errors.New("use native Tencent speed; natural-language instruction is unsupported")}
	}
	q := make(url.Values)
	if v := native.Speed; v != nil {
		if math.IsNaN(*v) || *v < minSpeed || *v > maxSpeed {
			return nil, invalid("native speed must be within [-2, 6]")
		}
		q.Set("Speed", strconv.FormatFloat(*v, 'f', -1, 64))
	}
	if v := native.Volume; v != nil {
		if math.IsNaN(*v) || *v < minVolume || *v > maxVolume {
			return nil, invalid("volume must be within [-10, 10]")
		}
		q.Set("Volume", strconv.FormatFloat(*v, 'f', -1, 64))
	}
	if native.EmotionCategory != "" {
		q.Set("EmotionCategory", native.EmotionCategory)
	}
	if v := native.EmotionIntensity; v != nil {
		if native.EmotionCategory == "" || *v < minEmotionIntensity || *v > maxEmotionIntensity {
			return nil, invalid("emotion intensity requires a category and must be within [50, 200]")
		}
		q.Set("EmotionIntensity", strconv.Itoa(*v))
	}
	if v := native.SegmentRate; v != nil {
		if *v < minSegmentRate || *v > maxSegmentRate {
			return nil, invalid("segment rate must be within [0, 2]")
		}
		q.Set("SegmentRate", strconv.Itoa(*v))
	}
	if native.FastVoiceType != "" {
		q.Set("FastVoiceType", native.FastVoiceType)
	}
	if native.EnableSubtitle != nil {
		q.Set("EnableSubtitle", strconv.FormatBool(*native.EnableSubtitle))
	}
	if native.EnableLatex != nil {
		q.Set("EnableLatex", strconv.FormatBool(*native.EnableLatex))
	}
	return q, nil
}
