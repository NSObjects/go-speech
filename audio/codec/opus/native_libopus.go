//go:build libopus && cgo

package opus

/*
#cgo pkg-config: opus
#include <opus/opus.h>
*/
import "C"

import (
    "encoding/binary"
    "fmt"
    "unsafe"
)

type libopusDecoder struct {
    ptr *C.OpusDecoder
    rate int
    channels int
}

// Available reports the compiled-in native support. The shared system
// libopus still needs to be installed on the target runtime.
func Available() bool {return true}

func openNative(sampleRate,channels int)(nativeDecoder,error){
    var status C.int
    ptr:=C.opus_decoder_create(C.opus_int32(sampleRate),C.int(channels),&status)
    if status!=C.OPUS_OK || ptr==nil{
        return nil,fmt.Errorf("opus: create decoder: %s",C.GoString(C.opus_strerror(status)))
    }
    return &libopusDecoder{ptr:ptr,rate:sampleRate,channels:channels},nil
}

func (n *libopusDecoder) decode(packet []byte)([]byte,error){
    // Opus packets may encode at most 120ms. This is a per-channel sample
    // count; opus_decode returns samples *per channel*, not total samples.
    maxSamples:=n.rate*120/1000
    samples:=make([]C.opus_int16,maxSamples*n.channels)
    count:=C.opus_decode(
        n.ptr,
        (*C.uchar)(unsafe.Pointer(&packet[0])),
        C.opus_int32(len(packet)),
        &samples[0],
        C.int(maxSamples),
        0,
    )
    if count<0{
        return nil,fmt.Errorf("%w: libopus error %s",ErrInvalidPacket,C.GoString(C.opus_strerror(C.int(count))))
    }
    output:=make([]byte,int(count)*n.channels*2)
    for i,sample:=range samples[:int(count)*n.channels]{
        binary.LittleEndian.PutUint16(output[i*2:],uint16(int16(sample)))
    }
    return output,nil
}
func (n *libopusDecoder) close(){
    if n.ptr!=nil{
        C.opus_decoder_destroy(n.ptr)
        n.ptr=nil
    }
}
