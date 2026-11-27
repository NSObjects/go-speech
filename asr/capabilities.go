package asr

import (
	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/capability"
)

// Capabilities describes the SDK's locally implemented recognition transport,
// not remote entitlements, model availability or recognition quality.
// Audio is a coarse format filter; use ValidateConfig for actual combinations.
type Capabilities struct {
	Provider   string
	Model      string
	ResourceID string
	Protocol   string
	Audio      audio.Capability
	Batch      bool
	Streaming  bool

	// Features describes selected advanced controls; absent entries are unknown.
	Features capability.Features
}

// CapabilityProvider is optional on ASR clients.
type CapabilityProvider interface {
	Capabilities() Capabilities
}

// ConfigValidator checks a concrete streaming configuration without network
// access or billing. Recognize additionally validates the complete audio buffer.
type ConfigValidator interface {
	ValidateConfig(Config) error
}

// Feature keys shared across provider capability snapshots.
const (
	FeatureEmotion              = "emotion"
	FeatureSpeechRate           = "speech_rate"
	FeatureVolume               = "volume"
	FeatureLanguageDetection    = "language_detection"
	FeatureGenderDetection      = "gender_detection"
	FeatureSpeakerDiarization   = "speaker_diarization"
	FeatureSpeakerContext       = "speaker_context"
	FeatureWordTimestamps       = "word_timestamps"
	FeatureSegmentTimestamps    = "segment_timestamps"
	FeatureContext              = "context"
	FeatureDynamicContext       = "dynamic_context"
	FeatureUsage                = "usage"
	FeatureImageContext         = "image_context"
	FeatureHotwords             = "hotwords"
	FeaturePunctuation          = "punctuation"
	FeatureTextNormalization    = "text_normalization"
	FeatureDisfluencyRemoval    = "disfluency_removal"
	FeatureSemanticSegmentation = "semantic_segmentation"
	FeatureSecondPass           = "second_pass"
	FeatureNoiseThreshold       = "noise_threshold"
	FeatureSensitiveWords       = "sensitive_words"
	FeatureDialect              = "dialect"
	FeatureAccelerate           = "accelerate"
)
