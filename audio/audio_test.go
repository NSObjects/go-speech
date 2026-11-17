package audio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"testing"
)

func pcmSamples(values ...int16) []byte {
	b := make([]byte, 2*len(values))
	for i, v := range values {
		binary.LittleEndian.PutUint16(b[i*2:], uint16(v))
	}
	return b
}
func TestWAVRoundTripAndInspection(t *testing.T) {
	src := Buffer{Format: Format{EncodingPCM16LE, 16000, 1}, Data: pcmSamples(-100, 0, 100)}
	wrapped, err := EncodeWAVPCM16(src)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Detect(wrapped.Data)
	if err != nil || got != EncodingWAV {
		t.Fatalf("detect: %v %v", got, err)
	}
	if err = ValidateBuffer(wrapped); err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeWAVPCM16(wrapped.Data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Format != src.Format || !bytes.Equal(decoded.Data, src.Data) {
		t.Fatalf("round trip mismatch: %#v vs %#v", decoded, src)
	}
	// A RIFF file can contain arbitrary chunks; decode must honor padding.
	base := wrapped.Data
	withJunk := make([]byte, 0, len(base)+12)
	withJunk = append(withJunk, base[:36]...)
	withJunk = append(withJunk, []byte("JUNK")...)
	size := make([]byte, 4)
	binary.LittleEndian.PutUint32(size, 3)
	withJunk = append(withJunk, size...)
	withJunk = append(withJunk, 'a', 'b', 'c', 0)
	withJunk = append(withJunk, base[36:]...)
	binary.LittleEndian.PutUint32(withJunk[4:], uint32(len(withJunk)-8))
	more, err := DecodeWAVPCM16(withJunk)
	if err != nil || !bytes.Equal(more.Data, src.Data) {
		t.Fatalf("unknown chunk: %v", err)
	}
	bad := append([]byte(nil), base...)
	binary.LittleEndian.PutUint16(bad[20:22], 3) // IEEE float (unsupported).
	if _, err = DecodeWAVPCM16(bad); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("expected PCM-only rejection: %v", err)
	}
	if _, err = DecodeWAVPCM16(base[:len(base)-1]); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("expected truncated RIFF: %v", err)
	}
	incorrect := wrapped
	incorrect.Format.SampleRate = 48000
	if err = ValidateBuffer(incorrect); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("expected metadata mismatch: %v", err)
	}
}
func TestDetectHeaders(t *testing.T) {
	ogg := func(magic string) []byte {
		raw := make([]byte, 27+1+len(magic))
		copy(raw, []byte("OggS"))
		raw[26] = 1
		raw[27] = byte(len(magic))
		copy(raw[28:], []byte(magic))
		return raw
	}
	cases := []struct {
		data []byte
		want Encoding
	}{
		{ogg("OpusHead"), EncodingOpus},
		{ogg("Speex   "), EncodingSpeex},
		{[]byte("#!AMR\nabc"), EncodingAMR},
		{[]byte("ID3\x04\x00"), EncodingMP3},
		{[]byte{0xff, 0xfb, 0x90, 0x64}, EncodingMP3},
		{[]byte{0xff, 0xf1, 0x50, 0x80, 0x00, 0x1f, 0xfc}, EncodingAAC},
	}
	for _, tc := range cases {
		enc, err := Detect(tc.data)
		if err != nil || enc != tc.want {
			t.Errorf("detect %x: %q, %v; want %q", tc.data, enc, err, tc.want)
		}
		if err = ValidateBuffer(Buffer{Data: tc.data, Format: Format{Encoding: tc.want, SampleRate: 16000, Channels: 1}}); err != nil {
			t.Errorf("validate %q: %v", tc.want, err)
		}
	}
	if _, err := Detect(pcmSamples(42, 0, 17)); !errors.Is(err, ErrUnknownEncoding) {
		t.Fatalf("raw PCM must not be guessed: %v", err)
	}
	if _, err := Detect([]byte("#!AMR-WB\n")); !errors.Is(err, ErrUnknownEncoding) {
		t.Fatalf("AMR-WB must not be treated as NB: %v", err)
	}
	if err := ValidateBuffer(Buffer{Data: []byte("raw"), Format: Format{Encoding: EncodingPCM16LE, SampleRate: 16000, Channels: 1}}); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("partial PCM frame must fail: %v", err)
	}
	if err := ValidateBuffer(Buffer{Data: []byte("#!AMR\n"), Format: Format{Encoding: EncodingOpus, SampleRate: 8000, Channels: 1}}); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("must reject codec mismatch: %v", err)
	}
}
func TestPCMConversion(t *testing.T) {
	mono := Format{EncodingPCM16LE, 16000, 1}
	stereo := Format{EncodingPCM16LE, 16000, 2}
	c, err := NewPCM16StreamConverter(mono, stereo)
	if err != nil {
		t.Fatal(err)
	}
	// Streaming input can split both samples and multi-byte frames.
	a, err := c.Process([]byte{0xe8})
	if err != nil || len(a) != 0 {
		t.Fatalf("expected incomplete frame, got %x %v", a, err)
	}
	b, err := c.Process([]byte{0x03, 0x18, 0xfc})
	if err != nil {
		t.Fatal(err)
	}
	want := pcmSamples(1000, 1000, -1000, -1000)
	if !bytes.Equal(b, want) {
		t.Fatalf("mono to stereo: got %x, want %x", b, want)
	}
	if _, err = c.Flush(); err != nil {
		t.Fatal(err)
	}
	x, err := NewPCM16StreamConverter(stereo, mono)
	if err != nil {
		t.Fatal(err)
	}
	averaged, err := x.Process(pcmSamples(30000, -30000, 32767, 32767))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(averaged, pcmSamples(0, 32767)) {
		t.Fatalf("stereo mix: %x", averaged)
	}
	_, _ = x.Flush()
	invalid, err := NewPCM16StreamConverter(stereo, mono)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = invalid.Process([]byte{1})
	if _, err = invalid.Flush(); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("truncated stream: %v", err)
	}
}
func TestPCMResamplingChunkInvariant(t *testing.T) {
	const inHz, outHz = 48000, 16000
	n := 4800
	wave := make([]byte, 2*n)
	for i := 0; i < n; i++ {
		v := int16(math.Round(10000 * math.Sin(2*math.Pi*1000*float64(i)/inHz)))
		binary.LittleEndian.PutUint16(wave[2*i:], uint16(v))
	}
	source := Buffer{Data: wave, Format: Format{EncodingPCM16LE, inHz, 1}}
	target := Format{EncodingPCM16LE, outHz, 1}
	whole, err := ConvertPCM16(source, target)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(whole.Data), n*outHz/inHz*2; got != want {
		t.Fatalf("got %d output bytes, want %d", got, want)
	}
	stream, err := NewPCM16StreamConverter(source.Format, target)
	if err != nil {
		t.Fatal(err)
	}
	var parts []byte
	for pos := 0; pos < len(wave); {
		end := pos + 13 + (pos % 31)
		if end > len(wave) {
			end = len(wave)
		}
		fragment, err := stream.Process(wave[pos:end])
		if err != nil {
			t.Fatal(err)
		}
		parts = append(parts, fragment...)
		pos = end
	}
	tail, err := stream.Flush()
	if err != nil {
		t.Fatal(err)
	}
	parts = append(parts, tail...)
	if !bytes.Equal(parts, whole.Data) {
		t.Fatal("PCM resampling depends on chunk sizes")
	}
	if _, err = stream.Process(wave); !errors.Is(err, ErrInvalidData) {
		t.Fatal("processed after flush")
	}
	// Verify the 1 kHz tone was not lost.
	peak := 0
	for i := 0; i < len(whole.Data); i += 2 {
		sample := int(int16(binary.LittleEndian.Uint16(whole.Data[i:])))
		if sample < 0 {
			sample = -sample
		}
		if sample > peak {
			peak = sample
		}
	}
	if peak < 8000 {
		t.Fatalf("unexpectedly attenuated speech-band signal: %d", peak)
	}
}
func TestPCMResamplingSuppressesAliasing(t *testing.T) {
	const inHz = 48000
	n := 4800
	pcm := make([]byte, n*2)
	for i := 0; i < n; i++ {
		// 12 kHz is above the target 16 kHz Nyquist of 8 kHz.
		v := int16(math.Round(12000 * math.Sin(2*math.Pi*12000*float64(i)/inHz)))
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(v))
	}
	out, err := ConvertPCM16(Buffer{Data: pcm, Format: Format{EncodingPCM16LE, inHz, 1}}, Format{EncodingPCM16LE, 16000, 1})
	if err != nil {
		t.Fatal(err)
	}
	sum := 0.0
	for i := 0; i < len(out.Data); i += 2 {
		s := float64(int16(binary.LittleEndian.Uint16(out.Data[i:])))
		sum += s * s
	}
	rms := math.Sqrt(sum / (float64(len(out.Data)) / 2))
	if rms > 2000 {
		t.Fatalf("aliasing attenuation insufficient: RMS %.1f", rms)
	}
}
func TestConvertWAV(t *testing.T) {
	pcm := Buffer{Data: pcmSamples(10, 20, 30, 40), Format: Format{EncodingPCM16LE, 16000, 1}}
	wav, err := EncodeWAVPCM16(pcm)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ConvertPCM16(wav, Format{EncodingWAV, 8000, 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = ValidateBuffer(result); err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeWAVPCM16(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Format.SampleRate != 8000 || len(decoded.Data) != 4 {
		t.Fatalf("wrong WAV conversion: %#v", decoded.Format)
	}
	_, err = ConvertPCM16(Buffer{Data: []byte("ID3xxxx"), Format: Format{EncodingMP3, 16000, 1}}, Format{EncodingPCM16LE, 16000, 1})
	if !errors.Is(err, ErrUnsupportedConversion) {
		t.Fatalf("expected no compressed decoder: %v", err)
	}
}
func TestCapability(t *testing.T) {
	cap := Capability{Encodings: []Encoding{EncodingPCM16LE, EncodingOpus}, SampleRates: []int{16000}, Channels: []int{1}}
	if !cap.Supports(Format{EncodingPCM16LE, 16000, 1}) {
		t.Fatal("reject valid")
	}
	for _, f := range []Format{
		{EncodingPCM16LE, 48000, 1},
		{EncodingMP3, 16000, 1},
		{EncodingOpus, 16000, 2},
	} {
		if err := cap.Validate(f); !errors.Is(err, ErrUnsupportedFormat) {
			t.Fatalf("format %v got %v", f, err)
		}
	}
}
