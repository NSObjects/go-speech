package volcengine

import (
	"math"
	"strings"
	"unicode/utf8"

	"github.com/NSObjects/go-speech/tts"
)

const (
	minMultiplier = 0.5
	maxMultiplier = 2
	minPitch = -12
	maxPitch = 12
	minSilenceMS = 0
	maxSilenceMS = 30000
)

// Options exposes Seed-TTS 3.0 native voice controls.
// Volume is a multiplier; Pitch is a semitone offset.
type Options struct {
	Volume          *float64
	Pitch           *int
	SimilarityBoost *bool
	DialectBoost    *bool

	// Instructions is the native ordered list. Do not combine with tts.Options.Instruction.
	Instructions []string
	// SessionID links serial requests; use a separate ID for independent conversations.
	SessionID    string
	LanguageCode string
	// BitRate is in bits per second. Zero preserves the existing default.
	BitRate int
	// PronunciationTone contains native source/replacement or source/(pinyin) rules.
	PronunciationTone    []string
	TextNormalization    *bool
	EnableMarkdownFilter *bool
	Watermark            *Watermark
	EnableSubtitle       *bool
	// SilenceDurationMS is HTTP-only, 0–30000 milliseconds.
	SilenceDurationMS *float64
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

func nativeOptions(o tts.Options) (Options, error) {
	var native Options
	switch p := o.Provider.(type) {
	case nil:
	case Options:
		native = p
	case *Options:
		if p == nil {
			return Options{}, invalid("nil provider options")
		}
		native = *p
	default:
		return Options{}, invalid("provider options do not belong to volcengine")
	}
	return native, nil
}

func settings(o tts.Options) (*voiceSettings, error) {
	native, err := nativeOptions(o)
	if err != nil {
		return nil, err
	}
	return buildVoiceSettings(o, native)
}

func buildVoiceSettings(o tts.Options, native Options) (*voiceSettings, error) {
	for _, v := range []*float64{o.Speed, native.Volume} {
		if v != nil && (math.IsNaN(*v) || *v < minMultiplier || *v > maxMultiplier) {
			return nil, invalid("speed and volume must be within [0.5, 2]")
		}
	}
	if native.Pitch != nil && (*native.Pitch < minPitch || *native.Pitch > maxPitch) {
		return nil, invalid("pitch must be within [-12, 12]")
	}
	if o.Instruction != "" && len(native.Instructions) != 0 {
		return nil, invalid("Instruction and native Instructions are mutually exclusive")
	}
	instructions := native.Instructions
	if o.Instruction != "" {
		instructions = []string{o.Instruction}
	}
	for _, instruction := range instructions {
		if strings.TrimSpace(instruction) == "" || !utf8.ValidString(instruction) {
			return nil, invalid("invalid instruction")
		}
	}
	if native.DialectBoost != nil && *native.DialectBoost && len(instructions) == 0 {
		return nil, invalid("dialect boost requires an instruction")
	}
	if o.Speed == nil && len(instructions) == 0 && native.Volume == nil && native.Pitch == nil && native.SimilarityBoost == nil && native.DialectBoost == nil {
		return nil, nil
	}
	s := &voiceSettings{
		Speed: o.Speed, Volume: native.Volume, Pitch: native.Pitch,
		SimilarityBoost: native.SimilarityBoost, DialectBoost: native.DialectBoost,
		Instructions: instructions,
	}
	return s, nil
}

// Watermark controls provider-native audible and embedded AIGC markers.
type Watermark struct {
	AIGCWatermark *bool         `json:"aigc_watermark,omitempty"`
	AIGCMetadata  *AIGCMetadata `json:"aigc_metadata,omitempty"`
}

// AIGCMetadata configures the service's embedded provenance marker.
type AIGCMetadata struct {
	Enable            *bool  `json:"enable,omitempty"`
	ContentProducer   string `json:"content_producer,omitempty"`
	ProduceID         string `json:"produce_id,omitempty"`
	ContentPropagator string `json:"content_propagator,omitempty"`
	PropagateID       string `json:"propagate_id,omitempty"`
}

type pronunciationDictionary struct {
	Tone []string `json:"tone"`
}

// advancedRequest is shared by the HTTP body and WebSocket init event.
type advancedRequest struct {
	PronunciationDictionary *pronunciationDictionary `json:"pronunciation_dictionary,omitempty"`
	TextNormalization       *bool                    `json:"text_normalization,omitempty"`
	EnableMarkdownFilter    *bool                    `json:"enable_markdown_filter,omitempty"`
	Watermark               *Watermark               `json:"watermark,omitempty"`
	EnableSubtitle          *bool                    `json:"enable_subtitle,omitempty"`
}

func advanced(native Options, output *outputFormat, duplex bool) (advancedRequest, error) {
	var req advancedRequest
	if native.BitRate != 0 {
		if output.Format == "pcm" {
			return req, invalid("bit rate is not supported for PCM")
		}
		if native.BitRate != 64000 && native.BitRate != 160000 {
			return req, invalid("bit rate must be 64000 or 160000 bps")
		}
		if output.Format == "mp3" && output.SampleRate == 8000 && native.BitRate != 64000 {
			return req, invalid("8000 Hz MP3 requires 64000 bps")
		}
		output.BitRate = native.BitRate
	}
	if v := native.SilenceDurationMS; v != nil {
		if duplex {
			return req, invalid("silence duration is supported only for complete text")
		}
		if math.IsNaN(*v) || *v < minSilenceMS || *v > maxSilenceMS {
			return req, invalid("silence duration must be within [0, 30000] ms")
		}
	}
	for _, rule := range native.PronunciationTone {
		source, target, ok := strings.Cut(rule, "/")
		if !ok || strings.TrimSpace(source) == "" || strings.TrimSpace(target) == "" || !utf8.ValidString(rule) {
			return req, invalid("invalid pronunciation rule: expected source/target")
		}
	}
	if len(native.PronunciationTone) != 0 {
		req.PronunciationDictionary = &pronunciationDictionary{Tone: native.PronunciationTone}
	}
	req.TextNormalization = native.TextNormalization
	req.EnableMarkdownFilter = native.EnableMarkdownFilter
	req.Watermark = native.Watermark
	req.EnableSubtitle = native.EnableSubtitle
	return req, nil
}
