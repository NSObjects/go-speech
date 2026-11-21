package speech

import (
	"errors"

	"github.com/NSObjects/go-speech/tts"
	aliyuntts "github.com/NSObjects/go-speech/tts/providers/aliyun"
	tencenttts "github.com/NSObjects/go-speech/tts/providers/tencent"
	volctts "github.com/NSObjects/go-speech/tts/providers/volcengine"
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
	TencentTTS    tencenttts.Config
	AliyunTTS     aliyuntts.Config
	VolcengineTTS volctts.Config
)

func (TencentTTS) isTTSConfig()    {}
func (AliyunTTS) isTTSConfig()     {}
func (VolcengineTTS) isTTSConfig() {}

// TTSSynthesizer supports both complete-buffer and streaming synthesis.
// All built-in providers implement both tts contracts.
type TTSSynthesizer interface {
	tts.Synthesizer
	tts.StreamSynthesizer
}

// NewTTS creates a provider-selected TTS client with no network connection.
// A new provider session is established only for Synthesize or SynthesizeStream.
// Both value and pointer configs are accepted; typed nil pointers are rejected.
func NewTTS(cfg TTSConfig) (TTSSynthesizer, error) {
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
	case VolcengineTTS:
		return newVolcengineTTS(volctts.Config(c))
	case *VolcengineTTS:
		if c != nil {
			return newVolcengineTTS(volctts.Config(*c))
		}
	}
	return nil, ErrInvalidTTSConfig
}

func newTencentTTS(cfg tencenttts.Config) (TTSSynthesizer, error) {
	client, err := tencenttts.New(cfg)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func newAliyunTTS(cfg aliyuntts.Config) (TTSSynthesizer, error) {
	client, err := aliyuntts.New(cfg)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func newVolcengineTTS(cfg volctts.Config) (TTSSynthesizer, error) {
	client, err := volctts.New(cfg)
	if err != nil {
		return nil, err
	}
	return client, nil
}
