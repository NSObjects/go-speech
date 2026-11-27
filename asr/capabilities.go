package asr

import "github.com/NSObjects/go-speech/audio"

// Capabilities describes the SDK's locally implemented recognition transport,
// not remote entitlements, model availability or recognition quality.
// Audio is a coarse format filter; use ValidateConfig for actual combinations.
type Capabilities struct {
	Provider  string
	Model     string
	Protocol  string
	Audio     audio.Capability
	Batch     bool
	Streaming bool
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
