package speech_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	speech "github.com/NSObjects/go-speech"
	"github.com/NSObjects/go-speech/tts"
)

func TestNewTTSSupportedProviders(t *testing.T) {
	for _, tc := range []struct {
		name string
		config speech.TTSConfig
	}{
		{
			"Tencent value",
			speech.TencentTTS{AppID: "123456", SecretID: "test-id", SecretKey: "test-key"},
		},
		{
			"Tencent pointer",
			&speech.TencentTTS{AppID: "123456", SecretID: "test-id", SecretKey: "test-key"},
		},
		{
			"Aliyun value",
			speech.AliyunTTS{
				APIKey: "test-key", Endpoint: "wss://dashscope.aliyuncs.com/api-ws/v1/inference",
			},
		},
		{
			"Aliyun pointer",
			&speech.AliyunTTS{
				APIKey: "test-key", Endpoint: "wss://dashscope.aliyuncs.com/api-ws/v1/inference",
			},
		},
		{"Volcengine value", speech.VolcengineTTS{APIKey: "test-key"}},
		{"Volcengine pointer", &speech.VolcengineTTS{APIKey: "test-key"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, err := speech.NewTTS(tc.config)
			if err != nil || client == nil {
				t.Fatalf("NewTTS(%T) = %T, %v", tc.config, client, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := client.SynthesizeStream(ctx, tts.Request{}); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled Stream = %v", err)
			}
			if _, err := client.Synthesize(ctx, tts.Request{}); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled Batch = %v", err)
			}
		})
	}
}

func TestNewTTSVariableSelectionAndNil(t *testing.T) {
	configs := map[string]speech.TTSConfig{
		"tencent": speech.TencentTTS{
			AppID: "123456", SecretID: "id", SecretKey: "key",
		},
		"volcengine": speech.VolcengineTTS{APIKey: "key"},
	}
	first, err := speech.NewTTS(configs["tencent"])
	if err != nil {
		t.Fatal(err)
	}
	second, err := speech.NewTTS(configs["volcengine"])
	if err != nil {
		t.Fatal(err)
	}
	if reflect.TypeOf(first) == reflect.TypeOf(second) {
		t.Fatalf("same provider type for different configs: %T", first)
	}
	var a *speech.AliyunTTS
	var v *speech.VolcengineTTS
	var tcfg *speech.TencentTTS
	for _, config := range []speech.TTSConfig{nil, a, v, tcfg} {
		client, err := speech.NewTTS(config)
		if client != nil || !errors.Is(err, speech.ErrInvalidTTSConfig) {
			t.Fatalf("invalid config %T returned client %T, error %v", config, client, err)
		}
	}
}

func TestNewTTSProviderValidationErrors(t *testing.T) {
	for _, config := range []speech.TTSConfig{
		speech.TencentTTS{AppID: "not-numeric", SecretID: "test", SecretKey: "test"},
		speech.AliyunTTS{APIKey: "key"},
		speech.VolcengineTTS{},
	} {
		client, err := speech.NewTTS(config)
		if err == nil || client != nil || errors.Is(err, speech.ErrInvalidTTSConfig) {
			t.Fatalf("invalid provider config %T = %T, %v", config, client, err)
		}
	}
}
