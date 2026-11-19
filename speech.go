// Package speech provides a small, vendor-independent entry point for
// constructing ASR clients. Session behavior lives in the asr package;
// provider-specific protocols remain in asr/providers.
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
		return tencent.New(tencent.Config(c))
	case *TencentASR:
		if c != nil {
			return tencent.New(tencent.Config(*c))
		}
	case AliyunASR:
		return aliyun.New(aliyun.Config(c))
	case *AliyunASR:
		if c != nil {
			return aliyun.New(aliyun.Config(*c))
		}
	case VolcengineASR:
		return volcengine.New(volcengine.Config(c))
	case *VolcengineASR:
		if c != nil {
			return volcengine.New(volcengine.Config(*c))
		}
	}
	return nil, ErrInvalidASRConfig
}
