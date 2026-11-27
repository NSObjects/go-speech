package ttscollect

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
)

type recvFunc func(context.Context) (audio.Chunk, error)

func (f recvFunc) Recv(ctx context.Context) (audio.Chunk, error) { return f(ctx) }

func sequence(chunks []audio.Chunk, terminal error) recvFunc {
	i := 0
	return func(context.Context) (audio.Chunk, error) {
		if i == len(chunks) {
			return audio.Chunk{}, terminal
		}
		chunk := chunks[i]
		i++
		return chunk, nil
	}
}

func TestCollect(t *testing.T) {
	format := audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1}
	chunks := []audio.Chunk{{Format: format, Data: []byte{1, 2}}, {Format: format, Data: []byte{3, 4}}}
	out, err := collect(context.Background(), sequence(chunks, io.EOF), "test", 4)
	if err != nil || out.Format != format || !bytes.Equal(out.Data, []byte{1, 2, 3, 4}) {
		t.Fatalf("result=%+v error=%v", out, err)
	}
	chunks[0].Data[0] = 9
	if out.Data[0] != 1 {
		t.Fatal("output aliases input chunk")
	}
}

func TestCollectRejectsInvalidBitstream(t *testing.T) {
	format := audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1}
	other := format
	other.SampleRate = 24000
	valid := audio.Chunk{Format: format, Data: []byte{1, 2}}
	for _, tc := range []struct {
		name   string
		chunks []audio.Chunk
		limit  int
		code   speecherr.Code
	}{
		{"empty stream", nil, 4, speecherr.CodeProtocol},
		{"empty chunk", []audio.Chunk{valid, {Format: format}}, 4, speecherr.CodeProtocol},
		{"format changed", []audio.Chunk{valid, {Format: other, Data: []byte{3, 4}}}, 4, speecherr.CodeProtocol},
		{"overflow", []audio.Chunk{valid, valid}, 3, speecherr.CodeInvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := collect(context.Background(), sequence(tc.chunks, io.EOF), "test", tc.limit)
			var detail *speecherr.Error
			if !errors.As(err, &detail) || detail.Code != tc.code || detail.Provider != "test" || detail.Retryable {
				t.Fatalf("unexpected error: %v", err)
			}
			if out.Format != (audio.Format{}) || out.Data != nil {
				t.Fatal("partial output returned on failure")
			}
		})
	}
}

func TestCollectPreservesTerminalError(t *testing.T) {
	cause := errors.New("connection failed")
	out, err := collect(context.Background(), sequence([]audio.Chunk{{Data: []byte{1, 2}}}, cause), "test", 4)
	if err != cause || out.Data != nil || out.Format != (audio.Format{}) {
		t.Fatalf("result=%+v error=%v", out, err)
	}
}

func TestCollectContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	out, err := collect(ctx, recvFunc(func(context.Context) (audio.Chunk, error) {
		calls++
		cancel()
		return audio.Chunk{Data: []byte{1, 2}}, nil
	}), "test", 4)
	if !errors.Is(err, context.Canceled) || out.Data != nil || calls != 1 {
		t.Fatalf("result=%+v error=%v calls=%d", out, err, calls)
	}
}
