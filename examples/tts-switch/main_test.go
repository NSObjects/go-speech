package main

import (
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
	t.Setenv("VOLCENGINE_TTS_MODEL_ID", "doubao-seed-tts-3.0")

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
				APIKey:  "volc-key",
				ModelID: "doubao-seed-tts-3.0",
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

