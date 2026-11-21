// Package speech provides vendor-independent constructors for ASR and TTS.
// Streaming contracts live in the asr and tts packages; provider-specific
// protocols remain in their respective providers packages.
//
// No configuration files, environment variables, global state or implicit
// network connections are managed by this package.
package speech

import (
	"errors"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/asr/providers/aliyun"
	"github.com/NSObjects/go-speech/asr/providers/tencent"
	"github.com/NSObjects/go-speech/asr/providers/volcengine"
)

// ErrInvalidASRConfig is returned when NewASR receives a nil or unsupported
// configuration type. Provider validation failures are returned unchanged.
var ErrInvalidASRConfig = errors.New("speech: invalid ASR configuration")

// ASRConfig is a sealed sum of the supported provider configuration types.
// A concrete type selects the provider; callers do not pass a redundant
// provider name or import its implementation package.
type ASRConfig interface {
	isASRConfig()
}

// Provider configuration types keep the exact fields of the existing
// adapters. These are defined types (not aliases) so they can satisfy the
// sealed ASRConfig interface without modifying adapter implementations.
type (
	TencentASR    tencent.Config
	AliyunASR     aliyun.Config
	VolcengineASR volcengine.Config
)

func (TencentASR) isASRConfig()    {}
func (AliyunASR) isASRConfig()     {}
func (VolcengineASR) isASRConfig() {}

// NewASR constructs the provider selected by the config's concrete type.
// It validates the chosen provider through its existing constructor and
// returns the shared streaming interface. No WebSocket is opened until
// OpenStream is called.
//
// Both values and pointers to the config types are supported. A typed nil
// pointer is rejected instead of panicking.
func NewASR(cfg ASRConfig) (asr.StreamRecognizer, error) {
	switch c := cfg.(type) {
	case TencentASR:
		return newTencentASR(tencent.Config(c))
	case *TencentASR:
		if c != nil {
			return newTencentASR(tencent.Config(*c))
		}
	case AliyunASR:
		return newAliyunASR(aliyun.Config(c))
	case *AliyunASR:
		if c != nil {
			return newAliyunASR(aliyun.Config(*c))
		}
	case VolcengineASR:
		return newVolcengineASR(volcengine.Config(c))
	case *VolcengineASR:
		if c != nil {
			return newVolcengineASR(volcengine.Config(*c))
		}
	}
	return nil, ErrInvalidASRConfig
}

// Each provider New returns *Client, whose nil pointer becomes non-nil when
// implicitly converted to asr.StreamRecognizer. Normalize errors before
// conversion so NewASR always returns a genuinely nil interface on failure.
func newTencentASR(cfg tencent.Config) (asr.StreamRecognizer, error) {
	client, err := tencent.New(cfg)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func newAliyunASR(cfg aliyun.Config) (asr.StreamRecognizer, error) {
	client, err := aliyun.New(cfg)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func newVolcengineASR(cfg volcengine.Config) (asr.StreamRecognizer, error) {
	client, err := volcengine.New(cfg)
	if err != nil {
		return nil, err
	}
	return client, nil
}
