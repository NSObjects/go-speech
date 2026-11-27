package volcengine

import (
	"time"

	"github.com/NSObjects/go-speech/asr"
)

// Options configures the optimized bigmodel_async endpoint per session.
// Pointer fields override constructor defaults only when supplied.
type Options struct {
	EnableNonStream        *bool
	ResultType             string
	EndWindowSize          *time.Duration
	EnableEmotionDetection *bool
	ShowSpeechRate         *bool
	ShowVolume             *bool
	EnableLID              *bool
	EnableGenderDetection  *bool
	EnableSpeakerInfo      *bool
	EnableAccelerateText   *bool
	AccelerateScore        *int
	OutputZHVariant        string
}

func (Options) ASRProviderOptions() {}

func (c Config) applyOptions(o asr.Options, req map[string]any) (string, bool, error) {
	var native Options
	switch p := o.Provider.(type) {
	case nil:
	case Options:
		native = p
	case *Options:
		if p == nil {
			return "", false, argumentError("nil provider options")
		}
		native = *p
	default:
		return "", false, argumentError("provider options do not belong to volcengine")
	}
	resultType, endWindow, secondPass := c.ResultType, c.EndWindowSize, c.EnableNonStream
	if native.ResultType != "" {
		resultType = native.ResultType
	}
	if resultType != ResultSingle && resultType != ResultFull {
		return "", false, argumentError("ResultType must be full or single")
	}
	if native.EndWindowSize != nil {
		endWindow = *native.EndWindowSize
	}
	if endWindow != 0 && (endWindow < 200*time.Millisecond || endWindow%time.Millisecond != 0) {
		return "", false, argumentError("EndWindowSize must be at least 200ms and a whole number of milliseconds")
	}
	if native.EnableNonStream != nil {
		secondPass = *native.EnableNonStream
	}
	req["result_type"], req["enable_nonstream"] = resultType, secondPass
	if endWindow != 0 {
		req["end_window_size"] = int64(endWindow / time.Millisecond)
	}
	for key, value := range map[string]*bool{
		"enable_punc": o.Punctuation, "enable_itn": o.TextNormalization,
		"enable_ddc": o.DisfluencyRemoval,
		"enable_emotion_detection": native.EnableEmotionDetection,
		"show_speech_rate": native.ShowSpeechRate, "show_volume": native.ShowVolume,
		"enable_lid": native.EnableLID, "enable_gender_detection": native.EnableGenderDetection,
		"enable_speaker_info": native.EnableSpeakerInfo,
		"enable_accelerate_text": native.EnableAccelerateText,
	} {
		if value != nil {
			req[key] = *value
		}
	}
	enabled := func(v *bool) bool { return v != nil && *v }
	if enabled(native.EnableSpeakerInfo) {
		if !secondPass {
			return "", false, argumentError("speaker clustering requires EnableNonStream")
		}
		req["ssd_version"] = "200"
	}
	if v := native.AccelerateScore; v != nil {
		if !enabled(native.EnableAccelerateText) || *v < 0 || *v > 20 {
			return "", false, argumentError("AccelerateScore requires EnableAccelerateText and must be within [0, 20]")
		}
		req["accelerate_score"] = *v
	}
	if native.OutputZHVariant != "" {
		switch native.OutputZHVariant {
		case "traditional", "tw", "hk":
			req["output_zh_variant"] = native.OutputZHVariant
		default:
			return "", false, argumentError("invalid OutputZHVariant")
		}
	}
	details := o.UtteranceDetails || enabled(native.EnableEmotionDetection) ||
		enabled(native.ShowSpeechRate) || enabled(native.ShowVolume) ||
		enabled(native.EnableLID) || enabled(native.EnableGenderDetection) ||
		enabled(native.EnableSpeakerInfo)
	return resultType, details, nil
}
