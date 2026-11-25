package speech

import (
	"errors"

	"github.com/NSObjects/go-speech/tts"
	aliyuntts "github.com/NSObjects/go-speech/tts/providers/aliyun"
	tencenttts "github.com/NSObjects/go-speech/tts/providers/tencent"
	volctts "github.com/NSObjects/go-speech/tts/providers/volcengine"
	"github.com/NSObjects/go-speech/tts/providers/volcengine/seed3"
)

// ErrInvalidTTSConfig indicates a nil or unsupported TTS configuration.
// Provider-specific validation failures are returned unchanged.
var ErrInvalidTTSConfig = errors.New("speech: invalid TTS configuration")

// TTSConfig selects a provider through its strong configuration type.
// The SDK never loads YAML, environment variables or global configuration.
type TTSConfig interface {
	isTTSConfig()
}

type (
	TencentTTS         tencenttts.Config
	AliyunTTS          aliyuntts.Config
	VolcengineTTS      volctts.Config
	VolcengineSeed3TTS seed3.Config
)

func (TencentTTS) isTTSConfig()         {}
func (AliyunTTS) isTTSConfig()          {}
func (VolcengineTTS) isTTSConfig()      {}
func (VolcengineSeed3TTS) isTTSConfig() {}

// TTSSynthesizer is retained as an alias for the shared tts.Client.
// New code can use tts.Client directly; both names describe the same API.
type TTSSynthesizer = tts.Client

// NewTTS creates a provider-selected TTS client with no network connection.
// A new provider session is established only for Synthesize or SynthesizeStream.
// Both value and pointer configs are accepted; typed nil pointers are rejected.
func NewTTS(cfg TTSConfig) (tts.Client, error) {
	switch c := cfg.(type) {
	case TencentTTS:
		return newTencentTTS(tencenttts.Config(c))
	case *TencentTTS:
		if c != nil {
			return newTencentTTS(tencenttts.Config(*c))
		}
	case AliyunTTS:
		return newAliyunTTS(aliyuntts.Config(c))
	case *AliyunTTS:
		if c != nil {
			return newAliyunTTS(aliyuntts.Config(*c))
		}
	case VolcengineSeed3TTS:
		return newVolcengineSeed3TTS(seed3.Config(c))
	case *VolcengineSeed3TTS:
		if c != nil {
			return newVolcengineSeed3TTS(seed3.Config(*c))
		}
	case VolcengineTTS:
		return newVolcengineTTS(volctts.Config(c))
	case *VolcengineTTS:
		if c != nil {
			return newVolcengineTTS(volctts.Config(*c))
		}
	}
	return nil, ErrInvalidTTSConfig
}

func newTencentTTS(cfg tencenttts.Config) (tts.Client, error) {
	client, err := tencenttts.New(cfg)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func newAliyunTTS(cfg aliyuntts.Config) (tts.Client, error) {
	client, err := aliyuntts.New(cfg)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func newVolcengineTTS(cfg volctts.Config) (tts.Client, error) {
	client, err := volctts.New(cfg)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func newVolcengineSeed3TTS(cfg seed3.Config) (tts.Client, error) {
	client, err := seed3.New(cfg)
	if err != nil {
		return nil, err
	}
	return client, nil
}
