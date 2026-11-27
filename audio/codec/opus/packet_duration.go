package opus

import (
	"fmt"
	"time"
)

// PacketDuration returns the duration of a complete raw Opus packet without
// decoding it. It validates RFC 6716 framing, not the encoded audio content.
func PacketDuration(packet []byte) (time.Duration, error) {
	samples, err := packetSamples48k(packet)
	return time.Duration(samples) * time.Second / 48000, err
}

// packetSamples48k reads the Opus TOC and packet framing without decoding
// audio. Opus granule increments are always measured in 48kHz samples.
// Validates the RFC 6716 packet framing (including code-3 VBR/padding).
func packetSamples48k(packet []byte) (uint32, error) {
	invalid := func(reason string) (uint32, error) {
		return 0, fmt.Errorf("%w: %s", ErrOggInvalidPacket, reason)
	}
	if len(packet) < 1 {
		return invalid("empty packet")
	}
	toc := packet[0]
	config := int(toc >> 3)
	var perFrame uint32
	switch {
	case config < 12: // SILK: 10, 20, 40 or 60ms
		perFrame = [...]uint32{480, 960, 1920, 2880}[config%4]
	case config < 16: // hybrid: 10 or 20ms
		perFrame = [...]uint32{480, 960}[config%2]
	default: // CELT: 2.5, 5, 10 or 20ms
		perFrame = [...]uint32{120, 240, 480, 960}[config%4]
	}
	count := 1
	remaining := len(packet) - 1
	switch toc & 3 {
	case 0:
		if remaining > 1275 {
			return invalid("frame exceeds 1275 bytes")
		}
	case 1:
		count = 2
		if remaining%2 != 0 || remaining/2 > 1275 {
			return invalid("invalid equal-size frame pair")
		}
	case 2:
		count = 2
		size, consumed, ok := frameLength(packet[1:])
		if !ok {
			return invalid("missing VBR frame length")
		}
		remaining -= consumed
		if size > remaining || size > 1275 || remaining-size > 1275 {
			return invalid("invalid VBR frame lengths")
		}
	case 3:
		if len(packet) < 2 {
			return invalid("missing frame count")
		}
		count = int(packet[1] & 0x3f)
		if count == 0 || count > 48 {
			return invalid("frame count out of range")
		}
		cursor := 2
		padding := 0
		if packet[1]&0x40 != 0 {
			for {
				if cursor >= len(packet) {
					return invalid("truncated packet padding")
				}
				n := int(packet[cursor])
				cursor++
				if n == 255 {
					padding += 254
				} else {
					padding += n
					break
				}
				if padding > len(packet)-cursor {
					return invalid("padding exceeds packet size")
				}
			}
		}
		if padding > len(packet)-cursor {
			return invalid("padding exceeds payload")
		}
		end := len(packet) - padding
		if packet[1]&0x80 == 0 {
			size := end - cursor
			if size%count != 0 || size/count > 1275 {
				return invalid("invalid CBR frame lengths")
			}
		} else {
			for i := 0; i < count-1; i++ {
				size, consumed, ok := frameLength(packet[cursor:end])
				if !ok {
					return invalid("invalid VBR length header")
				}
				cursor += consumed
				if size > 1275 || size > end-cursor {
					return invalid("invalid VBR frame size")
				}
				cursor += size
			}
			if end-cursor > 1275 {
				return invalid("last VBR frame exceeds limit")
			}
		}
	}
	total := uint32(count) * perFrame
	if total == 0 || total > 5760 {
		return invalid("packet duration exceeds 120ms")
	}
	return total, nil
}

func frameLength(data []byte) (int, int, bool) {
	if len(data) == 0 {
		return 0, 0, false
	}
	if data[0] < 252 {
		return int(data[0]), 1, true
	}
	if len(data) < 2 {
		return 0, 0, false
	}
	return int(data[0]) + 4*int(data[1]), 2, true
}
