package opus

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"os/exec"
	"testing"

	"github.com/NSObjects/go-speech/audio"
)

type oggPageInfo struct {
	flags   byte
	granule uint64
	serial  uint32
	seq     uint32
	payload []byte
}

func parseTestOggPages(t *testing.T, data []byte) []oggPageInfo {
	t.Helper()
	var pages []oggPageInfo
	for at := 0; at < len(data); {
		if len(data)-at < 27 || !bytes.Equal(data[at:at+4], []byte("OggS")) {
			t.Fatalf("bad page at %d", at)
		}
		segments := int(data[at+26])
		if len(data)-at < 27+segments {
			t.Fatal("truncated segment table")
		}
		payloadLen := 0
		for _, v := range data[at+27 : at+27+segments] {
			payloadLen += int(v)
		}
		end := at + 27 + segments + payloadLen
		if end > len(data) {
			t.Fatalf("truncated payload at %d", at)
		}
		page := append([]byte(nil), data[at:end]...)
		want := binary.LittleEndian.Uint32(page[22:26])
		for i := 22; i < 26; i++ {
			page[i] = 0
		}
		if got := oggChecksum(page); got != want {
			t.Fatalf("bad CRC at %d: got %08x want %08x", at, got, want)
		}
		pages = append(pages, oggPageInfo{
			flags:   data[at+5],
			granule: binary.LittleEndian.Uint64(data[at+6 : at+14]),
			serial:  binary.LittleEndian.Uint32(data[at+14 : at+18]),
			seq:     binary.LittleEndian.Uint32(data[at+18 : at+22]),
			payload: append([]byte(nil), data[at+27+segments:end]...),
		})
		at = end
	}
	return pages
}

func TestOggMuxerHeadersPagesAndEOS(t *testing.T) {
	m, err := NewOggMuxer(OggConfig{Channels: 1, InputSampleRate: 16000, PreSkip: 312})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.WritePacket([]byte{0x08, 0x11}); !errors.Is(err, ErrOggHeaders) {
		t.Fatalf("missing headers: %v", err)
	}
	header, err := m.Headers()
	if err != nil {
		t.Fatal(err)
	}
	if again, err := m.Headers(); err != nil || len(again) != 0 {
		t.Fatalf("headers reemitted: %v, %d", err, len(again))
	}
	// TOC 0x08 => SILK 20ms => 960 samples at 48 kHz.
	packet := []byte{0x08, 0x15, 0x22, 0x55}
	encoded, err := m.WritePacket(packet)
	if err != nil {
		t.Fatal(err)
	}
	tail, err := m.Close()
	if err != nil {
		t.Fatal(err)
	}
	combined := append(append(append([]byte(nil), header...), encoded...), tail...)
	pages := parseTestOggPages(t, combined)
	if len(pages) != 4 {
		t.Fatalf("got %d pages; want 4", len(pages))
	}
	if pages[0].flags != 0x02 || pages[1].flags != 0 || pages[2].flags != 0 || pages[3].flags != 0x04 {
		t.Fatalf("unexpected flags: %v", []byte{pages[0].flags, pages[1].flags, pages[2].flags, pages[3].flags})
	}
	if !bytes.HasPrefix(pages[0].payload, []byte("OpusHead")) || !bytes.HasPrefix(pages[1].payload, []byte("OpusTags")) {
		t.Fatal("missing Opus headers")
	}
	if binary.LittleEndian.Uint16(pages[0].payload[10:12]) != 312 {
		t.Fatal("wrong preskip")
	}
	if binary.LittleEndian.Uint32(pages[0].payload[12:16]) != 16000 {
		t.Fatal("wrong original sample rate")
	}
	if !bytes.Equal(pages[2].payload, packet) {
		t.Fatal("muxer changed raw Opus packet bytes")
	}
	if pages[2].granule != 960 || pages[3].granule != 960 || m.GranulePosition() != 960 {
		t.Fatalf("unexpected 48k granule positions: %d %d", pages[2].granule, pages[3].granule)
	}
	serial := pages[0].serial
	for i, page := range pages {
		if page.seq != uint32(i) || page.serial != serial {
			t.Fatalf("page %d has wrong sequence/serial", i)
		}
	}
	if result, err := m.Close(); err != nil || len(result) != 0 {
		t.Fatalf("Close should be idempotent: %v", err)
	}
	if _, err := m.WritePacket(packet); !errors.Is(err, ErrOggClosed) {
		t.Fatalf("WritePacket after close: %v", err)
	}
}

func TestPacketDurationAndFraming(t *testing.T) {
	for _, tc := range []struct {
		p    []byte
		want uint32
	}{
		{[]byte{0x00}, 480},                                      // 10ms SILK
		{[]byte{0x08}, 960},                                      // 20ms SILK
		{[]byte{0x18}, 2880},                                     // 60ms SILK
		{[]byte{0x60}, 480},                                      // hybrid 10ms
		{[]byte{0x68}, 960},                                      // hybrid 20ms
		{[]byte{0x80}, 120},                                      // CELT 2.5ms
		{[]byte{0x88}, 240},                                      // CELT 5ms
		{[]byte{0x90}, 480},                                      // CELT 10ms
		{[]byte{0x98}, 960},                                      // CELT 20ms
		{[]byte{0x99, 0xaa, 0xbb}, 1920},                         // 2 frames CBR
		{[]byte{0x9a, 0x01, 0xaa, 0xbb}, 1920},                   // 2 frames VBR
		{[]byte{0x9b, 0x03, 0xaa, 0xbb, 0xcc}, 2880},             // 3 CBR
		{[]byte{0x9b, 0x83, 0x01, 0xaa, 0x01, 0xbb, 0xcc}, 2880}, // 3 VBR
	} {
		got, err := packetSamples48k(tc.p)
		if err != nil || got != tc.want {
			t.Errorf("TOC %x: got %d, err %v; want %d", tc.p, got, err, tc.want)
		}
	}
	for _, p := range [][]byte{
		nil, {0x9a}, {0x9b, 0}, {0x9b, 0x40}, // bad counts and padding
		{0x9b, 0x03, 0x00},  // bad CBR length (1 payload byte for 3 frames)
		{0x9b, 0x43, 255},   // truncated long padding
		{0x9b, 0x83, 10, 1}, // first VBR frame overflows payload
		{0x9b, 0x3f},        // 63 frames exceed 120ms
		{0x99, 0},           // CBR pair length not divisible by two
	} {
		if _, err := packetSamples48k(p); !errors.Is(err, ErrOggInvalidPacket) {
			t.Errorf("invalid Opus packet %x accepted: %v", p, err)
		}
	}
}

func TestOggMuxerValidation(t *testing.T) {
	for _, c := range []OggConfig{
		{}, {Channels: 3, InputSampleRate: 16000}, {Channels: 1, InputSampleRate: -1},
		{Channels: 1, InputSampleRate: int(math.MaxUint32) + 1},
		{Channels: 1, InputSampleRate: 16000, Vendor: string(bytes.Repeat([]byte{'A'}, 1025))},
	} {
		if _, err := NewOggMuxer(c); !errors.Is(err, audio.ErrInvalidFormat) {
			t.Errorf("invalid config %+v accepted: %v", c, err)
		}
	}
	m, err := NewOggMuxer(OggConfig{Channels: 1, InputSampleRate: 16000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Close(); !errors.Is(err, ErrOggNoPackets) {
		t.Fatalf("empty stream: %v", err)
	}
	_, _ = m.Headers()
	if _, err := m.Close(); !errors.Is(err, ErrOggNoPackets) {
		t.Fatalf("empty after header: %v", err)
	}
	if _, err := m.WritePacket(bytes.Repeat([]byte{0x08}, 61201)); !errors.Is(err, ErrOggInvalidPacket) {
		t.Fatalf("oversize packet: %v", err)
	}
}

func TestOggExact255Lacing(t *testing.T) {
	m, err := NewOggMuxer(OggConfig{Channels: 2, InputSampleRate: 48000})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = m.Headers()
	packet := make([]byte, 255)
	packet[0] = 0x98
	page, err := m.WritePacket(packet)
	if err != nil {
		t.Fatal(err)
	}
	if page[26] != 2 || page[27] != 255 || page[28] != 0 {
		t.Fatalf("bad terminating lacing values: %v", page[26:29])
	}
}

// FFmpeg should decode the muxed output using a proper Ogg demuxer, proving
// the stream is interoperable beyond the tests' own CRC/page parser.
func TestOggMuxerFFmpegRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg optional")
	}
	pcm := make([]byte, 16000*2)
	for i := 0; i < 16000; i++ {
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(int16(i%256-128)))
	}
	enc := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "s16le",
		"-ar", "16000", "-ac", "1", "-i", "pipe:0", "-c:a", "libopus",
		"-f", "ogg", "pipe:1")
	enc.Stdin = bytes.NewReader(pcm)
	original, err := enc.Output()
	if err != nil {
		t.Skipf("FFmpeg libopus encoder unavailable: %v", err)
	}
	pages := parseTestOggPages(t, original)
	if len(pages) < 3 || !bytes.HasPrefix(pages[0].payload, []byte("OpusHead")) {
		t.Fatal("invalid FFmpeg Opus fixture")
	}
	preSkip := binary.LittleEndian.Uint16(pages[0].payload[10:12])
	m, err := NewOggMuxer(OggConfig{Channels: 1, InputSampleRate: 16000, PreSkip: preSkip})
	if err != nil {
		t.Fatal(err)
	}
	remuxed, err := m.Headers()
	if err != nil {
		t.Fatal(err)
	}
	packetCount := 0
	for _, page := range pages[2:] {
		// The FFmpeg-generated fixture has one complete Opus packet per
		// lacing sequence. Ogg may aggregate several such packets per page.
		_ = page
	}
	// Parse Ogg lacing from the original instead of assuming page boundaries.
	for off := 0; off < len(original); {
		count := int(original[off+26])
		table := original[off+27 : off+27+count]
		pos := off + 27 + count
		dataPos := pos
		var packet []byte
		for _, size := range table {
			packet = append(packet, original[dataPos:dataPos+int(size)]...)
			dataPos += int(size)
			if size < 255 {
				if bytes.HasPrefix(packet, []byte("OpusHead")) || bytes.HasPrefix(packet, []byte("OpusTags")) {
					packet = nil
					continue
				}
				part, err := m.WritePacket(packet)
				if err != nil {
					t.Fatal(err)
				}
				remuxed = append(remuxed, part...)
				packetCount++
				packet = nil
			}
		}
		if len(packet) != 0 {
			t.Fatal("fixture contains Opus packet continued across Ogg pages; test parser needs continuation")
		}
		off = dataPos
	}
	if packetCount == 0 {
		t.Fatal("no audio packets extracted")
	}
	eos, err := m.Close()
	if err != nil {
		t.Fatal(err)
	}
	remuxed = append(remuxed, eos...)
	parsed := parseTestOggPages(t, remuxed)
	if len(parsed) != packetCount+3 {
		t.Fatalf("unexpected pages after remux: %d", len(parsed))
	}
	dec := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-i", "pipe:0",
		"-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", "-f", "s16le", "pipe:1")
	dec.Stdin = bytes.NewReader(remuxed)
	output, err := dec.CombinedOutput()
	if err != nil {
		t.Fatalf("FFmpeg rejected generated Ogg stream: %v: %s", err, output)
	}
	if len(output) < 12000 {
		t.Fatalf("too little decoded PCM: %d", len(output))
	}
}
