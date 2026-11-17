package ffmpeg

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os/exec"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/audio"
)

func compressedTone(t *testing.T, binaryName, encoder, muxer string) []byte {
	t.Helper()
	raw := tone()
	cmd := exec.Command(binaryName,
		"-hide_banner", "-loglevel", "error", "-nostdin",
		"-f", "s16le", "-ar", "16000", "-ac", "1", "-i", "pipe:0",
		"-c:a", encoder, "-f", muxer, "pipe:1")
	cmd.Stdin = bytes.NewReader(raw.Data)
	result, err := cmd.Output()
	if err != nil {
		t.Skipf("optional encoder %s unavailable: %v", encoder, err)
	}
	if len(result) == 0 {
		t.Fatal("encoder returned empty data")
	}
	return result
}

// silentAMRNB generates 20ms AMR-NB frames with zero-coded 12.2kbps payload.
// It tests the demuxer/stream boundary without relying on a proprietary AMR
// encoder being installed in the CI environment.
func silentAMRNB(frames int) []byte {
	out := []byte("#!AMR\n")
	for i := 0; i < frames; i++ {
		out = append(out, 0x3c) // FT=7, Q=1, 31 encoded bytes.
		out = append(out, make([]byte, 31)...)
	}
	return out
}

func TestStreamDecoderCompressedFragments(t *testing.T) {
	binaryName := checkFFmpeg(t)
	cases := []struct {
		name string
		enc  audio.Encoding
		data func(*testing.T) []byte
		rate int
	}{
		{"ogg-opus", audio.EncodingOpus, func(t *testing.T) []byte {
			return compressedTone(t, binaryName, "libopus", "ogg")
		}, 48000},
		{"ogg-speex", audio.EncodingSpeex, func(t *testing.T) []byte {
			return compressedTone(t, binaryName, "libspeex", "ogg")
		}, 16000},
		{"mp3", audio.EncodingMP3, func(t *testing.T) []byte {
			return compressedTone(t, binaryName, "libmp3lame", "mp3")
		}, 16000},
		{"aac", audio.EncodingAAC, func(t *testing.T) []byte {
			return compressedTone(t, binaryName, "aac", "adts")
		}, 16000},
		{"amr-nb", audio.EncodingAMR, func(t *testing.T) []byte {
			return silentAMRNB(50)
		}, 8000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			encoded := tc.data(t)
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			target := audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1}
			session, err := OpenStreamDecoder(ctx, StreamConfig{
				Input:  audio.Format{Encoding: tc.enc, SampleRate: tc.rate, Channels: 1},
				Output: target,
				Binary: binaryName,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			if session.OutputFormat() != target {
				t.Fatalf("incorrect output metadata: %v", session.OutputFormat())
			}
			type result struct {
				pcm []byte
				err error
			}
			done := make(chan result, 1)
			go func() {
				var output []byte
				buf := make([]byte, 8192)
				for {
					n, err := session.Read(ctx, buf)
					if n > 0 {
						output = append(output, buf[:n]...)
					}
					if errors.Is(err, io.EOF) {
						done <- result{pcm: output}
						return
					}
					if err != nil {
						done <- result{err: err}
						return
					}
				}
			}()
			for offset := 0; offset < len(encoded); {
				end := offset + 1 + (offset % 199)
				if end > len(encoded) {
					end = len(encoded)
				}
				n, err := session.Write(ctx, encoded[offset:end])
				if err != nil {
					t.Fatalf("write compressed data at %d: %v", offset, err)
				}
				if n <= 0 {
					t.Fatal("write made no progress")
				}
				offset += n
			}
			if err := session.CloseInput(); err != nil {
				t.Fatal(err)
			}
			if err := session.CloseInput(); err != nil {
				t.Fatalf("CloseInput must be idempotent: %v", err)
			}
			if _, err := session.Write(ctx, []byte{42}); !errors.Is(err, ErrStreamInputClosed) {
				t.Fatalf("write after CloseInput: %v", err)
			}
			select {
			case decoded := <-done:
				if decoded.err != nil {
					t.Fatal(decoded.err)
				}
				if len(decoded.pcm) < 8000 || len(decoded.pcm)%2 != 0 {
					t.Fatalf("bad decoded PCM length %d", len(decoded.pcm))
				}
				// A one-second input should preserve approximately one second,
				// including the Opus/MP3 decoder padding and codec priming.
				if len(decoded.pcm) > 48000 {
					t.Fatalf("unexpected output duration: %d", len(decoded.pcm))
				}
				if tc.enc != audio.EncodingAMR {
					// Sine tone survives lossy codec decoding.
					var nonzero bool
					for i := 0; i+2 <= len(decoded.pcm); i += 2 {
						if int16(binary.LittleEndian.Uint16(decoded.pcm[i:])) != 0 {
							nonzero = true
							break
						}
					}
					if !nonzero {
						t.Fatal("decoder unexpectedly returned silence")
					}
				}
			case <-ctx.Done():
				t.Fatal("stream decoder did not reach EOF", ctx.Err())
			}
		})
	}
}

// TestStreamDecoderEmitsBeforeEndOfInput proves this is a *streaming*
// decoder rather than an append-all-and-convert implementation.
func TestStreamDecoderEmitsBeforeEndOfInput(t *testing.T) {
	binaryName := checkFFmpeg(t)
	raw := tone()
	longPCM := bytes.Repeat(raw.Data, 8) // Eight seconds of continuous speech-band audio.
	cmd := exec.Command(binaryName, "-hide_banner", "-loglevel", "error", "-nostdin",
		"-f", "s16le", "-ar", "16000", "-ac", "1", "-i", "pipe:0",
		"-c:a", "libopus", "-b:a", "32k", "-f", "ogg", "pipe:1")
	cmd.Stdin = bytes.NewReader(longPCM)
	encoded, err := cmd.Output()
	if err != nil {
		t.Skipf("optional libopus encoder unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	stream, err := OpenStreamDecoder(ctx, StreamConfig{
		Input: audio.Format{Encoding: audio.EncodingOpus, SampleRate: 48000, Channels: 1},
		Output: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
		Binary: binaryName,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	firstPCM := make(chan struct{})
	readDone := make(chan error, 1)
	go func() {
		seen := false
		buf := make([]byte, 4096)
		for {
			n, err := stream.Read(ctx, buf)
			if n > 0 && !seen {
				seen = true
				close(firstPCM)
			}
			if errors.Is(err, io.EOF) {
				readDone <- nil
				return
			}
			if err != nil {
				readDone <- err
				return
			}
		}
	}()
	writeAll := func(data []byte) {
		t.Helper()
		for len(data) > 0 {
			n, err := stream.Write(ctx, data)
			if err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				t.Fatal("compressed input write stalled")
			}
			data = data[n:]
		}
	}
	mid := len(encoded) / 2
	writeAll(encoded[:mid])
	select {
	case <-firstPCM:
		// PCM arrived while we intentionally kept input open and paused.
	case err := <-readDone:
		t.Fatalf("unexpected early decoder termination: %v", err)
	case <-time.After(4 * time.Second):
		t.Fatal("no PCM produced before CloseInput; decoder is not incremental")
	}
	writeAll(encoded[mid:])
	if err := stream.CloseInput(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-readDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestStreamDecoderEarlyCloseAndCancellation(t *testing.T) {
	binaryName := checkFFmpeg(t)
	format := audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1}
	if _, err := OpenStreamDecoder(context.Background(), StreamConfig{
		Input:  format,
		Output: audio.Format{Encoding: audio.EncodingWAV, SampleRate: 16000, Channels: 1},
	}); !errors.Is(err, audio.ErrUnsupportedConversion) {
		t.Fatalf("expected PCM-only stream output: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	session, err := OpenStreamDecoder(ctx, StreamConfig{Input: format, Output: format, Binary: binaryName})
	if err != nil {
		t.Fatal(err)
	}
	// Cancel a blocked Read without waiting for any input.
	readCtx, stopRead := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer stopRead()
	done := make(chan error, 1)
	go func() {
		_, err := session.Read(readCtx, make([]byte, 32))
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("blocked Read must honor cancellation: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("blocked Read did not terminate", ctx.Err())
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close not idempotent: %v", err)
	}
	if _, err := session.Write(context.Background(), []byte{1, 0}); !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("write after Close: %v", err)
	}
}

func TestStreamDecoderMalformedInputReportsExit(t *testing.T) {
	binaryName := checkFFmpeg(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := OpenStreamDecoder(ctx, StreamConfig{
		Input:  audio.Format{Encoding: audio.EncodingOpus, SampleRate: 48000, Channels: 1},
		Output: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
		Binary: binaryName,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Write(ctx, []byte("not an ogg stream")); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseInput(); err != nil {
		t.Fatal(err)
	}
	_, err = s.Read(ctx, make([]byte, 256))
	if err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("malformed input should return FFmpeg process error, got %v", err)
	}
}
