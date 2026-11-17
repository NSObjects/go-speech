//go:build integration

package aliyun

import (
	"bytes"
	"errors"
	"fmt"
)

// extractRawOpusPackets reads an Ogg Opus *test fixture* and extracts codec
// packets, skipping OpusHead and OpusTags. Ogg lacing may split a packet
// across pages, so the remainder is retained until the terminating segment.
// This is a test helper, not a production Ogg demuxer or RTP parser.
func extractRawOpusPackets(ogg []byte) ([][]byte, error) {
	var packets [][]byte
	var pending []byte
	headerPackets := 0
	for offset := 0; offset < len(ogg); {
		if len(ogg)-offset < 27 || !bytes.Equal(ogg[offset:offset+4], []byte("OggS")) || ogg[offset+4] != 0 {
			return nil, fmt.Errorf("invalid Ogg page at offset %d", offset)
		}
		count := int(ogg[offset+26])
		if len(ogg)-offset < 27+count {
			return nil, fmt.Errorf("truncated Ogg lacing table at %d", offset)
		}
		table := ogg[offset+27 : offset+27+count]
		payloadLength := 0
		for _, n := range table {
			payloadLength += int(n)
		}
		payloadAt := offset + 27 + count
		if len(ogg)-payloadAt < payloadLength {
			return nil, fmt.Errorf("truncated Ogg payload at %d", offset)
		}
		payload := ogg[payloadAt : payloadAt+payloadLength]
		at := 0
		for _, segment := range table {
			end := at + int(segment)
			pending = append(pending, payload[at:end]...)
			at = end
			if segment == 255 {
				continue
			}
			switch headerPackets {
			case 0:
				if !bytes.HasPrefix(pending, []byte("OpusHead")) {
					return nil, errors.New("Ogg missing OpusHead packet")
				}
			case 1:
				if !bytes.HasPrefix(pending, []byte("OpusTags")) {
					return nil, errors.New("Ogg missing OpusTags packet")
				}
			default:
				if len(pending) > 0 {
					packets = append(packets, pending)
				}
			}
			headerPackets++
			pending = nil
		}
		offset = payloadAt + payloadLength
	}
	if len(pending) != 0 {
		return nil, errors.New("incomplete final Ogg packet")
	}
	if len(packets) == 0 {
		return nil, errors.New("Ogg fixture contains no raw Opus audio packets")
	}
	return packets, nil
}
