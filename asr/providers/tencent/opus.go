package tencent

import (
	"encoding/binary"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/audio/codec/opus"
)

// EncodingOpusFrames identifies Tencent's native ASR framing, not Ogg Opus.
// Each frame is ASCII "opus", a uint16 little-endian payload size, then one
// complete 40ms mono Opus packet encoded at 16 kHz.
const EncodingOpusFrames audio.Encoding = "tencent_asr_opus"

const (
	nativeOpusDuration = 40 * time.Millisecond
	nativeOpusHeader   = 6
	// A bounded packet size matching the verified encoder path. Longer
	// RFC 6716 packets may be valid in general but are not advertised here.
	maxNativeOpusPacket = 1275
	maxNativeOpusFrames = 1500
)

// FrameOpusPacket adds Tencent's six-byte ASR header without changing or
// decoding the packet. The caller supplies one complete mono 40ms packet
// from a 16 kHz encoder. Encoder sample rate cannot be inferred from a TOC.
func FrameOpusPacket(packet []byte) ([]byte, error) {
	if err := validateNativeOpusPacket(packet); err != nil {
		return nil, err
	}
	frame := make([]byte, nativeOpusHeader+len(packet))
	copy(frame, "opus")
	binary.LittleEndian.PutUint16(frame[4:6], uint16(len(packet)))
	copy(frame[nativeOpusHeader:], packet)
	return frame, nil
}

func validateNativeOpusPacket(packet []byte) error {
	if len(packet) == 0 || len(packet) > maxNativeOpusPacket || packet[0]&4 != 0 {
		return argumentError("Tencent Opus requires a bounded mono packet")
	}
	duration, err := opus.PacketDuration(packet)
	if err != nil || duration != nativeOpusDuration {
		return argumentError("Tencent Opus requires a complete 40ms packet")
	}
	return nil
}

func nativeOpusPacket(frame []byte) ([]byte, error) {
	if len(frame) < nativeOpusHeader || string(frame[:4]) != "opus" {
		return nil, argumentError("invalid Tencent Opus frame header")
	}
	size := int(binary.LittleEndian.Uint16(frame[4:6]))
	if size != len(frame)-nativeOpusHeader {
		return nil, argumentError("invalid Tencent Opus frame size")
	}
	packet := frame[nativeOpusHeader:]
	if err := validateNativeOpusPacket(packet); err != nil {
		return nil, err
	}
	return packet, nil
}

func nativeOpusFrames(data []byte) ([][]byte, error) {
	if len(data) == 0 || len(data) > asr.MaxShortAudioBytes {
		return nil, argumentError("invalid short Tencent Opus audio size")
	}
	frames := make([][]byte, 0, min(len(data)/nativeOpusHeader, maxNativeOpusFrames))
	for len(data) > 0 {
		if len(frames) == maxNativeOpusFrames {
			return nil, argumentError("Tencent Opus short audio exceeds 60 seconds")
		}
		if len(data) < nativeOpusHeader || string(data[:4]) != "opus" {
			return nil, argumentError("invalid Tencent Opus frame header")
		}
		size := int(binary.LittleEndian.Uint16(data[4:6]))
		if size == 0 || size > maxNativeOpusPacket || size > len(data)-nativeOpusHeader {
			return nil, argumentError("invalid Tencent Opus frame size")
		}
		end := nativeOpusHeader + size
		if _, err := nativeOpusPacket(data[:end]); err != nil {
			return nil, err
		}
		frames = append(frames, data[:end])
		data = data[end:]
	}
	return frames, nil
}
