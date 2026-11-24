//go:build integration

package aliyun

import (
 "context"
 "os"
 "strings"
 "testing"
 "time"

 "github.com/NSObjects/go-speech/asr"
 "github.com/NSObjects/go-speech/audio"
)

func TestAliyunLiveShortRecognize(t *testing.T) {
 key, path, model := os.Getenv("DASHSCOPE_API_KEY"), os.Getenv("ASR_TEST_PCM_FILE"), os.Getenv("DASHSCOPE_ASR_MODEL")
 if key == "" || path == "" || model == "" { t.Fatal("Aliyun integration credentials, fixture, and model required") }
 rate := 16000
 if model == ModelParaformerRealtime8K { rate = 8000 }
 raw, err := os.ReadFile(path)
 if err != nil { t.Fatal(err) }
 cfg := Config{APIKey:key, Endpoint:os.Getenv("DASHSCOPE_WS_ENDPOINT"), Model:model}
 c,err := New(cfg)
 if err != nil { t.Fatal(err) }
 ctx,cancel := context.WithTimeout(context.Background(),90*time.Second)
 defer cancel()
 result,err := c.Recognize(ctx,audio.Buffer{Data:raw,Format:audio.Format{Encoding:audio.EncodingPCM16LE,SampleRate:rate,Channels:1}},asr.Config{})
 if err != nil { t.Fatal(err) }
 if strings.TrimSpace(result.Text)=="" { t.Fatalf("no final transcription for %s",model) }
 t.Logf("Aliyun short Recognize model=%s characters=%d",model,len([]rune(result.Text)))
}
