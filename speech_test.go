package speech_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	speech "github.com/NSObjects/go-speech"
	"github.com/NSObjects/go-speech/asr"
)

func TestNewASRSupportedProviders(t *testing.T) {
	tests := []struct {
		name   string
		config speech.ASRConfig
	}{
		{
			name: "Tencent value",
			config: speech.TencentASR{
				AppID: "123456", SecretID: "test-secret-id",
				SecretKey: "test-secret-key", EngineModel: "16k_zh_en_2.0",
			},
		},
		{
			name: "Tencent pointer",
			config: &speech.TencentASR{
				AppID: "123456", SecretID: "test-secret-id", SecretKey: "test-secret-key",
			},
		},
		{
			name: "Aliyun value",
			config: speech.AliyunASR{
				APIKey: "test-api-key",
				Endpoint: "wss://dashscope.aliyuncs.com/api-ws/v1/inference",
			},
		},
		{
			name: "Aliyun pointer",
			config: &speech.AliyunASR{
				APIKey: "test-api-key",
				Endpoint: "wss://dashscope.aliyuncs.com/api-ws/v1/inference",
			},
		},
		{
			name: "Volcengine value",
			config: speech.VolcengineASR{APIKey: "test-api-key"},
		},
		{
			name: "Volcengine pointer",
			config: &speech.VolcengineASR{APIKey: "test-api-key"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recognizer, err := speech.NewASR(tt.config)
			if err != nil {
				t.Fatalf("NewASR: %v", err)
			}
			if recognizer == nil {
				t.Fatal("NewASR returned nil recognizer")
			}
			// Constructors validate configuration, but do not open sockets.
			// An already canceled context must prevent opening a stream.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := recognizer.OpenStream(ctx, asr.Config{}); !errors.Is(err, context.Canceled) {
				t.Fatalf("OpenStream with canceled context: got %v", err)
			}
		})
	}
}

func TestNewASRSelectionByVariable(t *testing.T) {
	configs := map[string]speech.ASRConfig{
		"tencent": speech.TencentASR{
			AppID: "123456", SecretID: "test-secret-id", SecretKey: "test-secret-key",
		},
		"volcengine": speech.VolcengineASR{APIKey: "test-api-key"},
	}
	first, err := speech.NewASR(configs["tencent"])
	if err != nil {
		t.Fatal(err)
	}
	second, err := speech.NewASR(configs["volcengine"])
	if err != nil {
		t.Fatal(err)
	}
	if reflect.TypeOf(first) == reflect.TypeOf(second) {
		t.Fatalf("provider config selection produced same client type: %T", first)
	}
}

func TestNewASRRejectsNilConfig(t *testing.T) {
	var tencentNil *speech.TencentASR
	var aliyunNil *speech.AliyunASR
	var volcNil *speech.VolcengineASR
	for _, cfg := range []speech.ASRConfig{nil, tencentNil, aliyunNil, volcNil} {
		recognizer, err := speech.NewASR(cfg)
		if !errors.Is(err, speech.ErrInvalidASRConfig) || recognizer != nil {
			t.Fatalf("NewASR(%T) = %T, %v; want nil, ErrInvalidASRConfig", cfg, recognizer, err)
		}
	}
}

func TestNewASRPreservesProviderErrors(t *testing.T) {
	for _, cfg := range []speech.ASRConfig{
		speech.TencentASR{AppID: "not-a-number", SecretID: "id", SecretKey: "secret"},
		speech.AliyunASR{APIKey: "key"}, // Endpoint required by existing adapter.
		speech.VolcengineASR{},         // APIKey required by existing adapter.
	} {
		client, err := speech.NewASR(cfg)
		if err == nil || client != nil {
			t.Fatalf("invalid %T: got client %T, error %v", cfg, client, err)
		}
		if errors.Is(err, speech.ErrInvalidASRConfig) {
			t.Fatalf("expected adapter-specific validation error, got %v", err)
		}
		if strings.Contains(err.Error(), "secret") {
			t.Fatalf("error must not reveal test credentials: %v", err)
		}
	}
}
