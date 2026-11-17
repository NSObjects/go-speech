package ffmpeg

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math"
	"os/exec"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/audio"
)

func tone() audio.Buffer {
	const rate = 16000
	data := make([]byte, rate*2)
	for i := 0; i < rate; i++ {
		v := int16(math.Round(8000 * math.Sin(2*math.Pi*440*float64(i)/rate)))
		binary.LittleEndian.PutUint16(data[i*2:], uint16(v))
	}
	return audio.Buffer{Data: data, Format: audio.Format{
		Encoding: audio.EncodingPCM16LE, SampleRate: rate, Channels: 1,
	}}
}

func checkFFmpeg(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg executable is optional")
	}
	return path
}

func TestDecodeCompressedToPCM(t *testing.T) {
	binaryName := checkFFmpeg(t)
	input := tone()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cases := []struct {
		name, encoder, muxer string
		encoding             audio.Encoding
	}{
		{"mp3", "libmp3lame", "mp3", audio.EncodingMP3},
		{"ogg-opus", "libopus", "ogg", audio.EncodingOpus},
		{"adts-aac", "aac", "adts", audio.EncodingAAC},
		{"ogg-speex", "libspeex", "ogg", audio.EncodingSpeex},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-f", "s16le",
				"-ar", "16000", "-ac", "1", "-i", "pipe:0",
				"-c:a", tc.encoder, "-f", tc.muxer, "pipe:1"}
			cmd := exec.CommandContext(ctx, binaryName, args...)
			cmd.Stdin = bytes.NewReader(input.Data)
			encoded, err := cmd.Output()
			if err != nil {
				t.Skipf("optional ffmpeg encoder %s unavailable: %v", tc.encoder, err)
			}
			compressed := audio.Buffer{Data: encoded, Format: audio.Format{
				Encoding: tc.encoding, SampleRate: 16000, Channels: 1,
			}}
			decoded, err := (Converter{Binary: binaryName}).Convert(ctx, compressed, audio.Format{
				Encoding: audio.EncodingPCM16LE, SampleRate: 8000, Channels: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := audio.ValidateBuffer(decoded); err != nil {
				t.Fatal(err)
			}
			// A 1-second tone remains approximately 1 second after resampling.
			if n := len(decoded.Data); n < 12000 || n > 20000 {
				t.Fatalf("unexpected audio length %d", n)
			}
			wav, err := (Converter{Binary: binaryName}).Convert(ctx, compressed, audio.Format{
				Encoding: audio.EncodingWAV, SampleRate: 16000, Channels: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := audio.ValidateBuffer(wav); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLimitsAndCancellation(t *testing.T) {
	binaryName := checkFFmpeg(t)
	input := tone()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := (Converter{Binary: binaryName, MaxInputBytes: 1}).Convert(ctx, input, input.Format); !errors.Is(err, ErrInputTooLarge) {
		t.Fatalf("input limit: %v", err)
	}
	if _, err := (Converter{Binary: binaryName, MaxOutputBytes: 128}).Convert(ctx, input, input.Format); !errors.Is(err, ErrOutputTooLarge) {
		t.Fatalf("output limit: %v", err)
	}
	if _, err := (Converter{Binary: binaryName, MaxOutputBytes: -1}).Convert(ctx, input, input.Format); !errors.Is(err, audio.ErrInvalidFormat) {
		t.Fatalf("negative limit: %v", err)
	}
	alreadyCanceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := (Converter{Binary: binaryName}).Convert(alreadyCanceled, input, input.Format); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
