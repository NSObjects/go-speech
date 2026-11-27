package tencent

import (
	"bytes"
	"encoding/binary"

	"github.com/NSObjects/go-speech/asr"
)

// mp3Frames splits a complete mono 16 kHz MPEG-2 Layer III bitstream.
// Each frame has 576 samples (36ms), independent of bitrate. Preserve the
// original bytes, including a bounded leading ID3v2 and trailing ID3v1 tag.
// No decoding or conversion is performed.
func mp3Frames(data []byte) ([][]byte, error) {
	if len(data) == 0 || len(data) > asr.MaxShortAudioBytes {
		return nil, argumentError("invalid short MP3 audio size")
	}
	const maxTag = 64 << 10
	const maxFrames = 60000 / 36
	start, end := 0, len(data)
	if bytes.HasPrefix(data, []byte("ID3")) {
		if len(data) < 10 || data[3] < 2 || data[3] > 4 || data[4] == 255 {
			return nil, argumentError("invalid ID3v2 header")
		}
		size := 0
		for _, b := range data[6:10] {
			if b&128 != 0 {
				return nil, argumentError("invalid ID3v2 size")
			}
			size = size<<7 | int(b)
		}
		start = 10 + size
		if data[3] == 4 && data[5]&16 != 0 {
			start += 10
		}
		if start > end || start > maxTag {
			return nil, argumentError("invalid or oversized ID3v2 tag")
		}
	}
	if end-start >= 128 && string(data[end-128:end-125]) == "TAG" {
		end -= 128
	}
	frames := make([][]byte, 0, min((end-start)/72, maxFrames))
	for offset := start; offset < end; {
		if len(frames) == maxFrames {
			return nil, argumentError("MP3 short audio exceeds 60 seconds")
		}
		size, err := mp3FrameSize(data[offset:end])
		if err != nil {
			return nil, err
		}
		next := offset + size
		if next > end {
			return nil, argumentError("truncated MP3 audio frame")
		}
		from, to := offset, next
		if offset == start {
			from = 0
		}
		if next == end {
			to = len(data)
		}
		frames = append(frames, data[from:to])
		offset = next
	}
	if len(frames) == 0 {
		return nil, argumentError("MP3 contains no audio frames")
	}
	return frames, nil
}

func mp3FrameSize(data []byte) (int, error) {
	if len(data) < 4 {
		return 0, argumentError("truncated MP3 header")
	}
	h := binary.BigEndian.Uint32(data[:4])
	if h>>21 != 0x7ff || (h>>19)&3 != 2 || (h>>17)&3 != 1 ||
		(h>>10)&3 != 2 || (h>>6)&3 != 3 {
		return 0, argumentError("MP3 requires mono 16 kHz MPEG-2 Layer III")
	}
	index := (h >> 12) & 15
	if index == 0 || index == 15 {
		return 0, argumentError("unsupported MP3 free or reserved bitrate")
	}
	// MPEG-2 Layer III bitrate table (kb/s), 576 samples per frame.
	bitrates := [...]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160}
	return 72000*bitrates[index]/16000 + int((h>>9)&1), nil
}
