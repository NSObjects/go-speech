//go:build libopus && cgo

package opus

import (
    "encoding/hex"
    "testing"
)

func BenchmarkRawOpusDecode20ms(b *testing.B){
    packet,err:=hex.DecodeString(rawOpusFixture)
    if err!=nil{b.Fatal(err)}
    d,err:=NewDecoder(16000,1)
    if err!=nil{b.Fatal(err)}
    defer d.Close()
    b.ReportAllocs()
    b.ResetTimer()
    for i:=0;i<b.N;i++{
        if _,err:=d.Decode(packet);err!=nil{b.Fatal(err)}
    }
}
