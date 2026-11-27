package tencent

import (
	"bytes"
	"testing"
)

func TestMP3FramePacingInput(t *testing.T) {
	// MPEG-2 Layer III, 16 kHz mono, 32kb/s => 144-byte frame.
	frame := make([]byte, 144)
	copy(frame, []byte{0xff, 0xf3, 0x48, 0xc0})
	if size, err := mp3FrameSize(frame); err != nil || size != 144 {
		t.Fatalf("frame size=%d err=%v", size, err)
	}
	tag := []byte{'I', 'D', '3', 4, 0, 0, 0, 0, 0, 1, 0}
	trailer := make([]byte, 128)
	copy(trailer, "TAG")
	raw := append(bytes.Clone(tag), frame...)
	raw = append(raw, frame...)
	raw = append(raw, trailer...)
	frames, err := mp3Frames(raw)
	if err != nil || len(frames) != 2 || !bytes.Equal(bytes.Join(frames, nil), raw) {
		t.Fatalf("MP3 framing: %v", err)
	}
	for _, bad := range [][]byte{nil, frame[:3], frame[:143], tag, append(bytes.Clone(frame), 1), bytes.Repeat(frame, 1667)} {
		if _, err := mp3Frames(bad); err == nil {
			t.Fatal("accepted invalid MP3 buffer")
		}
	}
	frames, err = mp3Frames(bytes.Repeat(frame, 1666))
	if err != nil || len(frames) != 1666 {
		t.Fatalf("60s bound: %v", err)
	}
	stereo := bytes.Clone(frame)
	stereo[3] = 0
	if _, err := mp3Frames(stereo); err == nil {
		t.Fatal("accepted stereo MP3")
	}
}
