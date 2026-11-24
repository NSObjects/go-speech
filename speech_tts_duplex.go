package speech

import (
	"github.com/NSObjects/go-speech/tts"
)

// NewDuplexTTS creates a provider client for incremental text input and
// streaming audio output. The existing NewTTS and tts.Client remain unchanged.
func NewDuplexTTS(cfg TTSConfig) (tts.DuplexSynthesizer, error) {
	client, err := NewTTS(cfg)
	if err != nil {
		return nil, err
	}
	duplex, ok := client.(tts.DuplexSynthesizer)
	if !ok {
		return nil, tts.ErrDuplexUnsupported
	}
	return duplex, nil
}
