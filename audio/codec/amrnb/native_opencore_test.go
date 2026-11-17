//go:build opencoreamr && cgo

package amrnb

import (
    "errors"
    "testing"
)

func TestNativeDecodeFrames(t *testing.T){
    d,err:=NewDecoder()
    if err!=nil{t.Fatal(err)}
    if !Available(){t.Fatal("native backend missing")}
    if f:=d.OutputFormat();f.SampleRate!=8000 || f.Channels!=1 {t.Fatalf("bad format: %v",f)}
    for i:=0;i<15;i++{
        pcm,err:=d.Decode(frame(7))
        if err!=nil{t.Fatal(err)}
        if len(pcm)!=320{t.Fatalf("decoded %d bytes",len(pcm))}
    }
    if _,err:=d.Decode([]byte{0xff});!errors.Is(err,ErrInvalidFrame){t.Fatal(err)}
    if err:=d.Close();err!=nil{t.Fatal(err)}
    if err:=d.Close();err!=nil{t.Fatal(err)}
    if _,err:=d.Decode(frame(7));!errors.Is(err,ErrClosed){t.Fatal(err)}
}
