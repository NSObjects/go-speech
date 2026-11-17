package aliyun

import (
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
    "time"

    "github.com/NSObjects/go-speech/asr"
    "github.com/NSObjects/go-speech/audio"
    "github.com/NSObjects/go-speech/speecherr"
    "github.com/gorilla/websocket"
)

func TestWebSocketRoundTripAllModels(t *testing.T) {
    models:=[]struct{name string;rate int}{
        {ModelQwenAudio31Streaming,16000},
        {ModelQwenAudio31Message,16000},
        {ModelFunASRRealtime,16000},
        {ModelParaformerRealtime,16000},
        {ModelParaformerRealtime8K,8000},
    }
    for _,m:=range models{
        t.Run(m.name,func(t *testing.T){
            observed:=make(chan error,1)
            server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
                ws,err:=(websocket.Upgrader{}).Upgrade(w,r,nil)
                if err!=nil{observed<-err;return}
                defer ws.Close()
                if got:=r.Header.Get("Authorization");got!="Bearer test-token"{observed<-fmt.Errorf("bad auth header %q",got);return}
                _,data,err:=ws.ReadMessage()
                if err!=nil{observed<-err;return}
                var command taskMessage
                if err=json.Unmarshal(data,&command);err!=nil{observed<-err;return}
                if command.Payload.Model!=m.name{observed<-fmt.Errorf("bad model %q",command.Payload.Model);return}
                if command.Payload.Parameters["sample_rate"]!=float64(m.rate){observed<-fmt.Errorf("bad rate %v",command.Payload.Parameters["sample_rate"]);return}
                id:=command.Header.TaskID
                if command.Header.Action!="run-task"||id==""{observed<-fmt.Errorf("bad start %#v",command.Header);return}
                send:=func(event string,output map[string]any) error{
                    return ws.WriteJSON(map[string]any{"header":map[string]any{"task_id":id,"event":event},"payload":map[string]any{"output":output}})
                }
                if err=send("task-started",nil);err!=nil{observed<-err;return}
                typ,chunk,err:=ws.ReadMessage()
                if err!=nil{observed<-err;return}
                if typ!=websocket.BinaryMessage||string(chunk)!="audio"{observed<-fmt.Errorf("bad audio frame: %d %q",typ,chunk);return}
                result:=func(text string,final bool,heartbeat bool) error{
                    return send("result-generated",map[string]any{"sentence":map[string]any{"text":text,"sentence_end":final,"heartbeat":heartbeat}})
                }
                if err=result("",false,true);err!=nil{observed<-err;return}
                if err=result("你",false,false);err!=nil{observed<-err;return}
                if err=result("你好",true,false);err!=nil{observed<-err;return}
                _,data,err=ws.ReadMessage()
                if err!=nil{observed<-err;return}
                if err=json.Unmarshal(data,&command);err!=nil{observed<-err;return}
                if command.Header.Action!="finish-task"||command.Header.TaskID!=id{observed<-fmt.Errorf("bad finish %#v",command.Header);return}
                if err=send("task-finished",nil);err!=nil{observed<-err;return}
                observed<-nil
            }))
            defer server.Close()
            endpoint:="ws"+strings.TrimPrefix(server.URL,"http")+"/api-ws/v1/inference"
            client,err:=New(Config{Endpoint:endpoint,APIKey:"test-token",Model:m.name})
            if err!=nil{t.Fatal(err)}
            ctx,cancel:=context.WithTimeout(context.Background(),3*time.Second)
            defer cancel()
            stream,err:=client.OpenStream(ctx,asr.Config{Format:audio.Format{Encoding:audio.EncodingPCM16LE,SampleRate:m.rate,Channels:1}})
            if err!=nil{t.Fatal(err)}
            defer stream.Close()
            if err=stream.Send(ctx,[]byte("audio"));err!=nil{t.Fatal(err)}
            if err=stream.CloseSend(ctx);err!=nil{t.Fatal(err)}
            if err=stream.CloseSend(ctx);err!=nil{t.Fatalf("CloseSend not idempotent: %v",err)}
            for i,want:=range []asr.Event{{Type:asr.EventPartial,Text:"你"},{Type:asr.EventFinal,Text:"你好"}}{
                ev,err:=stream.Recv(ctx)
                if err!=nil{t.Fatalf("event %d: %v",i,err)}
                if ev!=want{t.Fatalf("event %d: got %#v, want %#v",i,ev,want)}
            }
            _,err=stream.Recv(ctx)
            if err!=io.EOF{t.Fatalf("want EOF, got %v",err)}
            if err:=<-observed;err!=nil{t.Fatal(err)}
        })
    }
}
func TestRejectNonLoopbackInsecure(t *testing.T){
    _,err:=New(Config{Endpoint:"ws://evil.example/api-ws/v1/inference",APIKey:"secret"})
    if !speecherr.IsCode(err,speecherr.CodeInvalidArgument){t.Fatalf("insecure endpoint: %v",err)}
}
