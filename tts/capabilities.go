package tts

import "github.com/NSObjects/go-speech/audio"

// Capabilities describes the SDK's implemented protocol paths. It does not
// confirm account entitlements or voice-level support. Audio is a coarse
// filter; ValidateRequest and ValidateDuplex check concrete combinations.
type Capabilities struct {
	Provider string
	Model    string
	Protocol string
	Audio    audio.Capability

	Batch     bool
	Streaming bool
	Duplex    bool
	Events    bool
	Flush     bool
	Reset     bool
	Cancel    bool
	// CompleteTextFormats and DuplexTextFormats describe accepted request
	// formats, not whether every model/voice interprets the markup.
	CompleteTextFormats []TextFormat
	DuplexTextFormats   []TextFormat
}

// CapabilityProvider is optional on TTS clients.
type CapabilityProvider interface {
	Capabilities() Capabilities
}

// RequestValidator checks complete-text streaming/detailed synthesis and
// duplex settings without opening a connection. ValidateSynthesize also
// checks audio-only batch restrictions. Text sent later is validated by SendText.
type RequestValidator interface {
	ValidateRequest(Request) error
	ValidateSynthesize(Request) error
	ValidateDuplex(DuplexRequest) error
}
