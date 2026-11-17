package audio

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
)

// DecodeWAVPCM16 reads a complete RIFF/WAVE file containing uncompressed
// integer 16-bit little-endian PCM. It rejects compressed/float WAV and
// truncated chunks. It copies PCM bytes into a caller-owned buffer.
func DecodeWAVPCM16(data []byte) (Buffer, error) {
	invalid := func(msg string) (Buffer, error) {
		return Buffer{}, fmt.Errorf("%w: %s", ErrInvalidData, msg)
	}
	if len(data) < 12 || !bytes.Equal(data[:4], []byte("RIFF")) || !bytes.Equal(data[8:12], []byte("WAVE")) {
		return invalid("missing RIFF/WAVE header")
	}
	total := uint64(binary.LittleEndian.Uint32(data[4:8])) + 8
	if total > uint64(len(data)) || total < 12 {
		return invalid("truncated RIFF container")
	}
	limit := int(total)
	var f Format
	var sample []byte
	hasFmt, hasData := false, false
	for at := 12; at+8 <= limit; {
		kind := data[at : at+4]
		size := uint64(binary.LittleEndian.Uint32(data[at+4 : at+8]))
		at += 8
		if size > uint64(limit-at) {
			return invalid("truncated WAV chunk")
		}
		end := at + int(size)
		switch {
		case bytes.Equal(kind, []byte("fmt ")):
			if hasFmt || size < 16 {
				return invalid("duplicate or invalid fmt chunk")
			}
			pcm := binary.LittleEndian.Uint16(data[at : at+2])
			channels := int(binary.LittleEndian.Uint16(data[at+2 : at+4]))
			hz := int(binary.LittleEndian.Uint32(data[at+4 : at+8]))
			byteRate := uint64(binary.LittleEndian.Uint32(data[at+8 : at+12]))
			align := int(binary.LittleEndian.Uint16(data[at+12 : at+14]))
			bits := binary.LittleEndian.Uint16(data[at+14 : at+16])
			if pcm != 1 || bits != 16 || channels == 0 || hz == 0 ||
				align != channels*2 || byteRate != uint64(hz)*uint64(align) {
				return invalid("only valid 16-bit PCM WAV is supported")
			}
			f = Format{Encoding: EncodingPCM16LE, SampleRate: hz, Channels: channels}
			hasFmt = true
		case bytes.Equal(kind, []byte("data")):
			if hasData {
				return invalid("duplicate WAV data chunk")
			}
			sample = data[at:end]
			hasData = true
		}
		at = end
		if size%2 == 1 {
			if at >= limit {
				return invalid("missing WAV padding byte")
			}
			at++
		}
	}
	if !hasFmt || !hasData {
		return invalid("missing fmt or data chunk")
	}
	if len(sample)%(2*f.Channels) != 0 {
		return invalid("unaligned WAV PCM data")
	}
	return Buffer{Data: append([]byte(nil), sample...), Format: f}, nil
}

// EncodeWAVPCM16 wraps raw signed PCM16LE frames in a standard 44-byte
// RIFF/WAVE header. It does not change the sample rate or channel layout.
func EncodeWAVPCM16(b Buffer) (Buffer, error) {
	if b.Format.Encoding != EncodingPCM16LE {
		return Buffer{}, fmt.Errorf("%w: WAV encoder expects pcm_s16le", ErrInvalidFormat)
	}
	if err := b.Format.Validate(); err != nil {
		return Buffer{}, err
	}
	if b.Format.Channels > math.MaxUint16 ||
		uint64(b.Format.SampleRate)*uint64(b.Format.Channels)*2 > math.MaxUint32 ||
		len(b.Data) > math.MaxUint32-36 {
		return Buffer{}, fmt.Errorf("%w: WAV limits exceeded", ErrInvalidFormat)
	}
	if len(b.Data)%(2*b.Format.Channels) != 0 {
		return Buffer{}, fmt.Errorf("%w: incomplete PCM frame", ErrInvalidData)
	}
	wav := make([]byte, 44+len(b.Data))
	copy(wav[0:4], "RIFF")
	binary.LittleEndian.PutUint32(wav[4:8], uint32(36+len(b.Data)))
	copy(wav[8:12], "WAVE")
	copy(wav[12:16], "fmt ")
	binary.LittleEndian.PutUint32(wav[16:20], 16)
	binary.LittleEndian.PutUint16(wav[20:22], 1)
	binary.LittleEndian.PutUint16(wav[22:24], uint16(b.Format.Channels))
	binary.LittleEndian.PutUint32(wav[24:28], uint32(b.Format.SampleRate))
	binary.LittleEndian.PutUint32(wav[28:32], uint32(b.Format.SampleRate*b.Format.Channels*2))
	binary.LittleEndian.PutUint16(wav[32:34], uint16(b.Format.Channels*2))
	binary.LittleEndian.PutUint16(wav[34:36], 16)
	copy(wav[36:40], "data")
	binary.LittleEndian.PutUint32(wav[40:44], uint32(len(b.Data)))
	copy(wav[44:], b.Data)
	return Buffer{Data: wav, Format: Format{Encoding: EncodingWAV, SampleRate: b.Format.SampleRate, Channels: b.Format.Channels}}, nil
}
