package main

import (
	"errors"
	"reflect"
	"testing"

	speech "github.com/NSObjects/go-speech"
)

// The example chooses the provider exclusively from caller-owned config;
// its credentials never leak into the shared Request or any SDK singleton.
func TestConfigForThreeTTSProviders(t *testing.T) {
	t.Setenv("DASHSCOPE_API_KEY", "ali-key")
	t.Setenv("DASHSCOPE_WS_ENDPOINT", "wss://dashscope.aliyuncs.com/api-ws/v1/inference")
	t.Setenv("TENCENT_APP_ID", "123456")
	t.Setenv("TENCENT_SECRET_ID", "tencent-id")
	t.Setenv("TENCENT_SECRET_KEY", "tencent-key")
	t.Setenv("VOLCENGINE_API_KEY", "volc-key")
	t.Setenv("VOLCENGINE_TTS_RESOURCE_ID", "seed-tts-2.0")

	for _, tc := range []struct {
		name string
		want speech.TTSConfig
	}{
		{
			"aliyun",
			speech.AliyunTTS{
				APIKey: "ali-key",
				Endpoint: "wss://dashscope.aliyuncs.com/api-ws/v1/inference",
			},
		},
		{
			"tencent",
			speech.TencentTTS{
				AppID: "123456",
				SecretID: "tencent-id",
				SecretKey: "tencent-key",
			},
		},
		{
			"volcengine",
			speech.VolcengineTTS{
				APIKey: "volc-key",
				ResourceID: "seed-tts-2.0",
			},
		},
	} {
		cfg, err := configFor(tc.name)
		if err != nil || !reflect.DeepEqual(cfg, tc.want) {
			t.Fatalf("%s: config=%#v, err=%v", tc.name, cfg, err)
		}
	}
	if cfg, err := configFor("unknown"); err == nil || cfg != nil {
		t.Fatalf("unknown provider: config=%v, err=%v", cfg, err)
	}
}

func TestModeOrStream(t *testing.T) {
	if got := modeOrStream(""); got != "stream" {
		t.Fatal("default mode should be stream")
	}
	if got := modeOrStream("batch"); got != "batch" {
		t.Fatal("batch mode changed")
	}
	if !errors.Is(errors.New("example"), errors.New("example")) {
		// errors.New has unique identity; no special error comparison is used
		// by the example beyond errors.Is(io.EOF).
	}
}
