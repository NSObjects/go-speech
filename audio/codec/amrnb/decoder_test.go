package amrnb

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

)

func frame(ft uint8) []byte {
	n, err := FrameSize(ft)
	if err != nil {
		panic(err)
	}
	f := make([]byte, n)
	f[0] = (ft << 3) | 4
	return f
}
func TestFrames(t *testing.T) {
	for ft := uint8(0); ft <= 8; ft++ {
		value := frame(ft)
		if err := ValidateFrame(value); err != nil {
			t.Fatalf("FT %d: %v", ft, err)
		}
	}
	if err := ValidateFrame(frame(15)); err != nil {
		t.Fatal(err)
	}
	for _, ft := range []uint8{9, 10, 11, 12, 13, 14, 16} {
		if _, err := FrameSize(ft); !errors.Is(err, ErrInvalidFrame) {
			t.Fatalf("FT=%d: %v", ft, err)
		}
	}
	bad := [][]byte{
		nil, {}, {0x3c}, {0xff}, {0x3d}, // footer padding bit is set
		{0xbc}, // upper reserved bit
	}
	for _, f := range bad {
		if err := ValidateFrame(f); !errors.Is(err, ErrInvalidFrame) {
			t.Fatalf("frame %x: %v", f, err)
		}
	}
}
func TestNoMandatoryNativeDependency(t *testing.T) {
	if Available() {
		t.Skip("native OpenCORE enabled")
	}
	d, err := NewDecoder()
	if d != nil || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("wanted ErrUnavailable, got %v", err)
	}
}

type mockSink struct {
	data     []byte
	finished int
	chunk    int
}

func (m *mockSink) Write(ctx context.Context, p []byte) (int, error) {
	n := len(p)
	if m.chunk > 0 && n > m.chunk {
		n = m.chunk
	}
	m.data = append(m.data, p[:n]...)
	return n, nil
}
func (m *mockSink) CloseInput() error { m.finished++; return nil }

func TestStreamFraming(t *testing.T) {
	sink := &mockSink{chunk: 2}
	f, err := NewFramer(sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.CloseInput(); !errors.Is(err, ErrNoFrames) {
		t.Fatal(err)
	}
	a, b := frame(7), frame(8)
	ctx := context.Background()
	if err := f.WriteFrame(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := f.WriteFrame(ctx, b); err != nil {
		t.Fatal(err)
	}
	want := append([]byte("#!AMR\n"), a...)
	want = append(want, b...)
	if !bytes.Equal(sink.data, want) {
		t.Fatalf("stream mismatch: %x", sink.data)
	}
	if err := f.CloseInput(); err != nil {
		t.Fatal(err)
	}
	if err := f.CloseInput(); err != nil {
		t.Fatal(err)
	}
	if sink.finished != 1 {
		t.Fatalf("CloseInput called %d times", sink.finished)
	}
	if err := f.WriteFrame(ctx, a); !errors.Is(err, ErrFramerClosed) {
		t.Fatal(err)
	}
	if _, err := NewFramer(nil); !errors.Is(err, ErrInvalidFrame) {
		t.Fatal(err)
	}
}

type zeroSink struct{}

func (zeroSink) Write(ctx context.Context, p []byte) (int, error) { return 0, nil }
func (zeroSink) CloseInput() error                                { return nil }
func TestShortWriteIsTerminal(t *testing.T) {
	f, _ := NewFramer(zeroSink{})
	err := f.WriteFrame(context.Background(), frame(7))
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("want short write, got %v", err)
	}
	if err2 := f.WriteFrame(context.Background(), frame(7)); !errors.Is(err2, io.ErrShortWrite) {
		t.Fatalf("retry must fail: %v", err2)
	}
}
