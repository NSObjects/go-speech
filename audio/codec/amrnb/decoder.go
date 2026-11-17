// Package amrnb handles AMR-NB file-storage frames (3GPP TS 26.101).
// Each frame begins with its one-byte FT/Q header and 0..31 payload bytes.
// It is NOT an RTP payload parser; octet-aligned RFC 4867 RTP packets have
// separate CMR/ToC handling and must be parsed before calling Decode.
//
// Native per-frame decoding is an opt-in CGo adapter (build tag opencoreamr).
// The pure-Go Framer can repackage frames as a continuous #!AMR stream for
// the optional FFmpeg decoder without any native compilation.
package amrnb

import (
    "errors"
    "fmt"
    "sync"

    "github.com/NSObjects/go-speech/audio"
)

var (
    ErrUnavailable = errors.New("amrnb: native OpenCORE support unavailable (build with -tags=opencoreamr and cgo)")
    ErrClosed = errors.New("amrnb: decoder closed")
    ErrInvalidFrame = errors.New("amrnb: invalid storage frame")
)

// AMR-NB 20ms frame payload lengths, without the one-byte frame header.
var sizes = [16]int{12,13,15,17,19,20,26,31,5,-1,-1,-1,-1,-1,-1,0}

// FrameSize returns the exact octet length of one AMR-NB file-storage frame
// (header included), or ErrInvalidFrame for reserved frame types.
func FrameSize(frameType uint8) (int,error) {
    if frameType>=uint8(len(sizes)) || sizes[frameType]<0{
        return 0,fmt.Errorf("%w: frame type %d",ErrInvalidFrame,frameType)
    }
    return 1+sizes[frameType],nil
}

// ValidateFrame accepts exactly one AMR-NB file-storage frame. FT is bits
// 6..3; Q is bit 2. The top bit and bottom two padding bits must be zero.
// It does not validate the speech coding payload or convert RTP framing.
func ValidateFrame(frame []byte) error {
    if len(frame)==0{return fmt.Errorf("%w: empty frame",ErrInvalidFrame)}
    if frame[0]&0x83!=0{
        return fmt.Errorf("%w: invalid frame header 0x%02x",ErrInvalidFrame,frame[0])
    }
    size,err:=FrameSize((frame[0]>>3)&0x0f)
    if err!=nil{return err}
    if len(frame)!=size{
        return fmt.Errorf("%w: want %d bytes, got %d",ErrInvalidFrame,size,len(frame))
    }
    return nil
}

type nativeDecoder interface {
    decode([]byte)([]byte,error)
    close()
}

// Decoder maintains OpenCORE AMR-NB state across successive 20ms storage
// frames and returns exactly 320 bytes of 8kHz mono PCM16LE per frame.
// It must not be shared between unrelated streams. Decode and Close are
// serialized; no FFmpeg subprocess is involved.
var _ audio.PacketDecoder = (*Decoder)(nil)

type Decoder struct {
    mu sync.Mutex
    backend nativeDecoder
    closed bool
}

func NewDecoder() (*Decoder,error){
    native,err:=openNative()
    if err!=nil{return nil,err}
    return &Decoder{backend:native},nil
}

func (d *Decoder) OutputFormat() audio.Format {
    return audio.Format{Encoding:audio.EncodingPCM16LE,SampleRate:8000,Channels:1}
}

// Decode accepts a single storage-format frame, NOT an RTP payload. If the
// header's Q bit is zero, the native decoder receives bad-frame indication.
func (d *Decoder) Decode(frame []byte)([]byte,error){
    if d==nil{return nil,ErrClosed}
    if err:=ValidateFrame(frame);err!=nil{return nil,err}
    d.mu.Lock()
    defer d.mu.Unlock()
    if d.closed{return nil,ErrClosed}
    return d.backend.decode(frame)
}
func (d *Decoder) Close()error{
    if d==nil{return nil}
    d.mu.Lock()
    defer d.mu.Unlock()
    if !d.closed{
        d.closed=true
        if d.backend!=nil{d.backend.close()}
    }
    return nil
}
