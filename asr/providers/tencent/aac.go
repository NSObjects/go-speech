package tencent

import "github.com/NSObjects/go-speech/asr"

// aacFrames splits the supported AAC-LC/ADTS subset without decoding.
// MPEG-4 AAC-LC uses 1024 samples per single-block frame, or 64ms at
// 16kHz. Only explicit mono channel configuration is supported here.
func aacFrames(data []byte) ([][]byte, error) {
	if len(data) == 0 || len(data) > asr.MaxShortAudioBytes {
		return nil, argumentError("invalid short AAC audio size")
	}
	const maxFrames = 60000 / 64
	frames := make([][]byte, 0, min(len(data)/7, maxFrames))
	for len(data) != 0 {
		if len(frames) == maxFrames {
			return nil, argumentError("AAC short audio exceeds 60 seconds")
		}
		size, err := aacFrameSize(data)
		if err != nil {
			return nil, err
		}
		frames = append(frames, data[:size])
		data = data[size:]
	}
	return frames, nil
}

func aacFrameSize(data []byte) (int, error) {
	if len(data) < 7 || data[0] != 0xff || data[1]&0xfe != 0xf0 {
		return 0, argumentError("AAC requires an MPEG-4 ADTS header")
	}
	// Profile 1 means AAC-LC (audio object type 2). Sampling frequency
	// index 8 means 16kHz. Reject PCE, stereo, and multiple raw blocks.
	profile := data[2] >> 6
	rate := (data[2] >> 2) & 15
	channels := ((data[2] & 1) << 2) | (data[3] >> 6)
	if profile != 1 || rate != 8 || channels != 1 || data[6]&3 != 0 {
		return 0, argumentError("AAC requires 16kHz mono AAC-LC and one raw block per ADTS frame")
	}
	header := 7
	if data[1]&1 == 0 {
		header = 9 // single-block frame with CRC
	}
	size := int(data[3]&3)<<11 | int(data[4])<<3 | int(data[5]>>5)
	if size <= header || size > len(data) {
		return 0, argumentError("invalid or truncated AAC ADTS frame")
	}
	return size, nil
}
