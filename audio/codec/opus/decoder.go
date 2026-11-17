// Package opus provides an optional native libopus decoder for *raw* Opus
// packets as received from a device or transport. It does not accept Ogg
// pages: Ogg Opus is handled separately by audio/ffmpeg.
//
// Importing this package does not require CGo. Native decoding is enabled by
// compiling with -tags=libopus and CGO_ENABLED=1 and installing libopus-dev
// (Linux) or opus via Homebrew (macOS). Without these requirements, NewDecoder
// returns ErrUnavailable instead of silently decoding incorrectly.
package opus

import (
    "errors"
    "fmt"
    "sync"

    "github.com/NSObjects/go-speech/audio"
)

var (
    ErrUnavailable = errors.New("opus: native libopus support unavailable (build with -tags=libopus and cgo)")
    ErrClosed = errors.New("opus: decoder closed")
    ErrInvalidPacket = errors.New("opus: invalid raw packet")
)

const maxOpusPacketBytes = 64 * 1024

// nativeDecoder is implemented by the optional libopus CGo binding.
// Each native instance holds codec state that must persist across packets.
type nativeDecoder interface {
    decode([]byte) ([]byte, error)
    close()
}

// Decoder consumes one complete raw Opus packet per Decode call; packet
// boundaries MUST be retained by the caller. Unlike Ogg Opus, raw packets
// have no OggS or OpusHead headers. The state is not reusable across
// independent audio sessions. Decode and Close are mutex-serialized.
var _ audio.PacketDecoder = (*Decoder)(nil)

type Decoder struct {
    mu sync.Mutex
    backend nativeDecoder
    format audio.Format
    closed bool
}

// NewDecoder configures a stateful native Opus decoder. libopus supports
// 8, 12, 16, 24, and 48 kHz, with mono or stereo PCM16LE output. The sampling
// rate is the *decoded PCM output rate*, not a codec packet header value.
func NewDecoder(sampleRate, channels int) (*Decoder,error) {
    switch sampleRate {
    case 8000,12000,16000,24000,48000:
    default:
        return nil, fmt.Errorf("%w: unsupported libopus PCM rate %d",audio.ErrInvalidFormat,sampleRate)
    }
    if channels!=1 && channels!=2 {
        return nil, fmt.Errorf("%w: Opus supports 1 or 2 channels",audio.ErrInvalidFormat)
    }
    backend,err:=openNative(sampleRate,channels)
    if err!=nil{return nil,err}
    return &Decoder{backend:backend, format:audio.Format{
        Encoding:audio.EncodingPCM16LE,SampleRate:sampleRate,Channels:channels,
    }},nil
}

// OutputFormat reports the PCM representation produced by Decode.
func (d *Decoder) OutputFormat() audio.Format {
    if d==nil{return audio.Format{}}
    return d.format
}

// Decode decodes one complete raw packet to PCM16LE. A packet may contain
// up to 120 ms of Opus audio. The returned slice is caller-owned. Passing
// raw Opus data as audio.EncodingOpus (Ogg) to an ASR provider is incorrect;
// use this packet decoder or a proper Ogg repackager first.
func (d *Decoder) Decode(packet []byte) ([]byte,error) {
    if d==nil{return nil,ErrClosed}
    d.mu.Lock()
    defer d.mu.Unlock()
    if d.closed{return nil,ErrClosed}
    if len(packet)==0 || len(packet)>maxOpusPacketBytes{
        return nil,fmt.Errorf("%w: packet length %d",ErrInvalidPacket,len(packet))
    }
    return d.backend.decode(packet)
}

// Close releases native state and is safe to call repeatedly, including
// concurrently with Decode. No finalizer is required: callers own the state.
func (d *Decoder) Close() error {
    if d==nil{return nil}
    d.mu.Lock()
    defer d.mu.Unlock()
    if !d.closed {
        d.closed=true
        if d.backend!=nil{d.backend.close()}
    }
    return nil
}
