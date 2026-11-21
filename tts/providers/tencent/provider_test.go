package tencent

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/NSObjects/go-speech/tts"
	"github.com/gorilla/websocket"
)

func mockServer(t *testing.T, run func(*websocket.Conn, *http.Request)) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request){
		ws,err := (&websocket.Upgrader{}).Upgrade(w,r,nil)
		if err != nil { t.Errorf("upgrade: %v",err); return }
		defer ws.Close()
		run(ws,r)
	}))
	t.Cleanup(srv.Close)
	return "ws"+strings.TrimPrefix(srv.URL,"http")+"/stream_wsv2"
}
func sendControl(ws *websocket.Conn, session string, code, ready, final int) error {
	data,err := json.Marshal(map[string]any{"code":code, "session_id":session,
		"message":"test", "ready":ready, "final":final})
	if err != nil { return err }
	return ws.WriteMessage(websocket.TextMessage,data)
}
func readCommand(ws *websocket.Conn, action, session, expectedText string) error {
	typ,raw,err := ws.ReadMessage()
	if err != nil { return err }
	if typ != websocket.TextMessage { return errors.New("expected text command") }
	var cmd struct {
		Session string `json:"session_id"`
		Message string `json:"message_id"`
		Action string `json:"action"`
		Data string `json:"data"`
	}
	if err := json.Unmarshal(raw,&cmd); err != nil { return err }
	if cmd.Session != session || cmd.Message == "" || cmd.Action != action || cmd.Data != expectedText {
		return errors.New("incorrect synthesis command")
	}
	return nil
}
func verifySignature(r *http.Request, secret string) bool {
	query := r.URL.Query()
	got := query.Get("Signature")
	if got == "" { return false }
	query.Del("Signature")
	keys := make([]string,0,len(query))
	for k := range query { keys = append(keys,k) }
	sort.Strings(keys)
	pairs := make([]string,0,len(keys))
	for _,k := range keys { pairs=append(pairs,k+"="+query.Get(k)) }
	mac := hmac.New(sha1.New,[]byte(secret))
	_,_ = mac.Write([]byte("GET"+r.Host+r.URL.Path+"?"+strings.Join(pairs,"&")))
	return hmac.Equal([]byte(got),[]byte(base64.StdEncoding.EncodeToString(mac.Sum(nil))))
}
func sampleRequest() tts.Request {
	return tts.Request{Text:"你好，欢迎使用语音合成。",Voice:"101001",
		Format:audio.Format{Encoding:audio.EncodingPCM16LE,SampleRate:16000,Channels:1}}
}
func TestStreamingAndBatch(t *testing.T) {
	checked := make(chan error,2)
	endpoint := mockServer(t,func(ws *websocket.Conn,r *http.Request){
		q := r.URL.Query()
		session := q.Get("SessionId")
		if !verifySignature(r,"test-secret") || q.Get("Action")!="TextToStreamAudioWSv2" ||
			q.Get("AppId")!="1250000000" || q.Get("VoiceType")!="101001" ||
			q.Get("SampleRate")!="16000" || q.Get("Codec")!="pcm" ||
			session=="" || q.Get("SecretId")!="test-id" {
			checked <- errors.New("invalid signed handshake")
			return
		}
		if err:=sendControl(ws,session,0,0,0);err!=nil { checked<-err;return }
		if err:=sendControl(ws,session,0,1,0);err!=nil { checked<-err;return }
		if err:=readCommand(ws,"ACTION_SYNTHESIS",session,sampleRequest().Text);err!=nil {checked<-err;return}
		if err:=readCommand(ws,"ACTION_COMPLETE",session,"");err!=nil {checked<-err;return}
		if err:=sendControl(ws,session,0,0,0);err!=nil {checked<-err;return}
		if err:=ws.WriteMessage(websocket.BinaryMessage,[]byte{1,2,3});err!=nil {checked<-err;return}
		if err:=ws.WriteMessage(websocket.BinaryMessage,[]byte{4,5});err!=nil {checked<-err;return}
		if err:=sendControl(ws,session,0,0,1);err!=nil {checked<-err;return}
		checked <- nil
	})
	client,err:=New(Config{AppID:"1250000000",SecretID:"test-id",SecretKey:"test-secret",Endpoint:endpoint})
	if err!=nil {t.Fatal(err)}
	ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second)
	defer cancel()
	s,err:=client.SynthesizeStream(ctx,sampleRequest())
	if err!=nil {t.Fatal(err)}
	for i,want:=range [][]byte{{1,2,3},{4,5}} {
		chunk,err:=s.Recv(ctx)
		if err!=nil || string(chunk.Data)!=string(want) || chunk.Format!=sampleRequest().Format {
			t.Fatalf("chunk %d: %+v, %v",i,chunk,err)
		}
	}
	for i:=0;i<2;i++ {
		_,err=s.Recv(context.Background())
		if !errors.Is(err,io.EOF) {t.Fatalf("EOF %d: %v",i,err)}
	}
	if err:=s.Close();err!=nil {t.Fatal(err)}
	out,err:=client.Synthesize(ctx,sampleRequest())
	if err!=nil || string(out.Data)!=string([]byte{1,2,3,4,5}) || out.Format!=sampleRequest().Format {
		t.Fatalf("batch: %+v %v",out,err)
	}
	for i:=0;i<2;i++ {
		if err:=<-checked;err!=nil {t.Fatal(err)}
	}
}
func TestRequestAndConfigValidation(t *testing.T) {
	for _,cfg:=range []Config{
		{},
		{AppID:"not-number",SecretID:"id",SecretKey:"secret"},
		{AppID:"1",SecretID:"id",SecretKey:"secret",Endpoint:"ws://example.com/stream_wsv2"},
		{AppID:"1",SecretID:"id",SecretKey:"secret",Endpoint:"wss://example.com/not-tts"},
	} {
		if _,err:=New(cfg);!speecherr.IsCode(err,speecherr.CodeInvalidArgument) {t.Fatalf("%+v: %v",cfg,err)}
	}
	for _,req:=range []tts.Request{
		{Voice:"101001"},
		{Text:"hello",Voice:"not-a-number"},
		{Text:"hello",Voice:"101001",Format:audio.Format{Encoding:audio.EncodingWAV,SampleRate:16000,Channels:1}},
		{Text:"hello",Voice:"101001",Format:audio.Format{Encoding:audio.EncodingMP3,SampleRate:44100,Channels:1}},
	} {
		_,_,err:=validateRequest(req)
		if err==nil {t.Fatalf("expected invalid request %+v",req)}
	}
	got,codec,err:=validateRequest(tts.Request{Text:"hello",Voice:"101001"})
	if err!=nil || codec!="mp3" || got.Format.SampleRate!=16000 {t.Fatalf("default: %+v %s %v",got,codec,err)}
	for _,tc:=range []struct{code int; want speecherr.Code; retry bool}{
		{10001,speecherr.CodeInvalidArgument,false},
		{10002,speecherr.CodeRateLimited,true},
		{10003,speecherr.CodeUnauthorized,false},
		{3022,speecherr.CodeRateLimited,false}, // live provider: resource pack exhausted
		{20000,speecherr.CodeUnavailable,true},
	} {
		err:=remoteError(tc.code,"failed")
		var e *speecherr.Error
		if !errors.As(err,&e) || e.Code!=tc.want || e.RemoteCode!=strconv.Itoa(tc.code) || e.Retryable!=tc.retry {
			t.Fatalf("remote %d: %v",tc.code,err)
		}
	}
}
func TestMissingCodeAndEarlyFinal(t *testing.T) {
	for _,raw:=range []string{
		`{"session_id":"session","ready":1}`,
		`{"session_id":"session","code":null,"ready":1}`,
		`{"session_id":"session","code":0,"final":2}`,
	} {
		if _,err:=decodeEvent([]byte(raw),"session");!speecherr.IsCode(err,speecherr.CodeProtocol) {
			t.Fatalf("invalid control %s: %v",raw,err)
		}
	}
	endpoint:=mockServer(t,func(ws *websocket.Conn,r *http.Request){
		_ = sendControl(ws,r.URL.Query().Get("SessionId"),0,0,1)
	})
	client,err:=New(Config{AppID:"1",SecretID:"id",SecretKey:"key",Endpoint:endpoint})
	if err!=nil {t.Fatal(err)}
	ctx,cancel:=context.WithTimeout(context.Background(),3*time.Second)
	defer cancel()
	if _,err=client.SynthesizeStream(ctx,sampleRequest());!speecherr.IsCode(err,speecherr.CodeProtocol) {
		t.Fatalf("early final: %v",err)
	}
}
func TestParentCancellationStopsRecv(t *testing.T) {
	ready:=make(chan struct{},1)
	endpoint:=mockServer(t,func(ws *websocket.Conn,r *http.Request){
		session:=r.URL.Query().Get("SessionId")
		if err:=sendControl(ws,session,0,1,0);err!=nil {return}
		if err:=readCommand(ws,"ACTION_SYNTHESIS",session,sampleRequest().Text);err!=nil {return}
		if err:=readCommand(ws,"ACTION_COMPLETE",session,"");err!=nil {return}
		ready<-struct{}{}
		_,_,_=ws.ReadMessage()
	})
	client,err:=New(Config{AppID:"1",SecretID:"id",SecretKey:"key",Endpoint:endpoint})
	if err!=nil {t.Fatal(err)}
	ctx,cancel:=context.WithCancel(context.Background())
	s,err:=client.SynthesizeStream(ctx,sampleRequest())
	if err!=nil {cancel();t.Fatal(err)}
	<-ready
	cancel()
	_,err=s.Recv(context.Background())
	if !errors.Is(err,context.Canceled) {t.Fatalf("canceled Recv: %v",err)}
	_,again:=s.Recv(context.Background())
	if !errors.Is(again,context.Canceled) {t.Fatalf("lost cancellation: %v",again)}
}
var _ = url.Values{}
