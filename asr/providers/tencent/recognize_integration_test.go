//go:build integration

package tencent

import (
 "context"
 "os"
 "strings"
 "testing"
 "time"

 "github.com/NSObjects/go-speech/asr"
 "github.com/NSObjects/go-speech/audio"
)

func TestTencentLiveShortRecognize(t *testing.T) {
 path:=os.Getenv("TENCENT_TEST_PCM_FILE")
 if path=="" {t.Fatal("TENCENT_TEST_PCM_FILE is required")}
 raw,err:=os.ReadFile(path)
 if err!=nil {t.Fatal(err)}
 model:=os.Getenv("TENCENT_ASR_MODEL")
 rate:=16000
 if strings.HasPrefix(model,"8k_"){rate=8000}
 client,err:=New(Config{
  AppID:os.Getenv("TENCENT_APP_ID"),SecretID:os.Getenv("TENCENT_SECRET_ID"),
  SecretKey:os.Getenv("TENCENT_SECRET_KEY"),EngineModel:model,NeedVAD:true,
 })
 if err!=nil {t.Fatal(err)}
 ctx,cancel:=context.WithTimeout(context.Background(),90*time.Second)
 defer cancel()
 result,err:=client.Recognize(ctx,audio.Buffer{Data:raw,Format:audio.Format{Encoding:audio.EncodingPCM16LE,SampleRate:rate,Channels:1}},asr.Config{})
 if err!=nil {t.Fatal(err)}
 if strings.TrimSpace(result.Text)==""{t.Fatal("Tencent Recognize returned no final text")}
 t.Logf("Tencent short Recognize model=%s chars=%d",model,len([]rune(result.Text)))
}
