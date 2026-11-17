package opus

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync"

	"github.com/NSObjects/go-speech/audio"
)

var (
	ErrOggClosed        = errors.New("opus: Ogg stream closed")
	ErrOggHeaders       = errors.New("opus: send Ogg headers before audio packets")
	ErrOggNoPackets     = errors.New("opus: Ogg stream contains no audio packets")
	ErrOggInvalidPacket = errors.New("opus: invalid raw Opus packet")
)

// OggConfig describes a fresh Ogg Opus logical stream. Channels must be 1 or 2.
// InputSampleRate is the device/encoder sample rate written into OpusHead;
// Ogg granule positions always use 48 kHz regardless of this value.
// PreSkip is encoder lookahead in 48kHz samples (0 only if it is unknown).
// Correct pre-skip depends on the encoder and is not inferred from packets.
type OggConfig struct {
	Channels        int
	InputSampleRate int
	PreSkip         uint16
	Vendor          string
}

// OggMuxer converts complete, sequential raw Opus codec packets into Ogg
// pages without decoding or re-encoding. Each packet yields one self-contained
// Ogg page, allowing the caller to forward it immediately to a streaming ASR.
// Headers returns the mandatory OpusHead and OpusTags pages first; Close emits
// an empty EOS page. The stream serial number is random for each instance.
//
// The caller must preserve raw Opus packet boundaries and order. Raw RTP
// payloads or transport headers are not valid input. OggMuxer is synchronized,
// though one producer per stream is preferred. Returned slices are caller-owned.
type OggMuxer struct {
	mu      sync.Mutex
	cfg     OggConfig
	serial  uint32
	pageSeq uint32
	granule uint64
	started bool
	closed  bool
	packets uint64
}

func NewOggMuxer(cfg OggConfig) (*OggMuxer, error) {
	if cfg.Channels != 1 && cfg.Channels != 2 {
		return nil, fmt.Errorf("%w: Opus supports mono or stereo", audio.ErrInvalidFormat)
	}
	if cfg.InputSampleRate <= 0 || cfg.InputSampleRate > math.MaxUint32 {
		return nil, fmt.Errorf("%w: invalid Opus input sample rate", audio.ErrInvalidFormat)
	}
	if len(cfg.Vendor) > 1024 {
		return nil, fmt.Errorf("%w: OpusTags vendor string too long", audio.ErrInvalidFormat)
	}
	if cfg.Vendor == "" {
		cfg.Vendor = "go-speech"
	}
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, fmt.Errorf("opus: random serial: %w", err)
	}
	return &OggMuxer{cfg: cfg, serial: binary.LittleEndian.Uint32(b[:])}, nil
}

// Headers emits the OpusHead (BOS) and OpusTags pages exactly once. Subsequent
// calls return nil. Send these bytes to ASR before the first WritePacket result.
func (m *OggMuxer) Headers() ([]byte, error) {
	if m == nil {
		return nil, ErrOggClosed
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrOggClosed
	}
	if m.started {
		return nil, nil
	}
	head := make([]byte, 19)
	copy(head, "OpusHead")
	head[8] = 1
	head[9] = byte(m.cfg.Channels)
	binary.LittleEndian.PutUint16(head[10:12], m.cfg.PreSkip)
	binary.LittleEndian.PutUint32(head[12:16], uint32(m.cfg.InputSampleRate))
	// Output gain is 0; mapping family 0 is mono/stereo.
	tags := make([]byte, 8+4+len(m.cfg.Vendor)+4)
	copy(tags, "OpusTags")
	binary.LittleEndian.PutUint32(tags[8:12], uint32(len(m.cfg.Vendor)))
	copy(tags[12:], m.cfg.Vendor)
	bos := m.makePage(0x02, 0, head)
	comment := m.makePage(0, 0, tags)
	m.started = true
	return append(bos, comment...), nil
}

// WritePacket emits an Ogg page containing a single raw Opus packet and
// advances the 48kHz granule position based on the TOC-derived duration.
// It does not accept Ogg pages; that format should be directly forwarded.
func (m *OggMuxer) WritePacket(packet []byte) ([]byte, error) {
	if m == nil {
		return nil, ErrOggClosed
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrOggClosed
	}
	if !m.started {
		return nil, ErrOggHeaders
	}
	if len(packet) > 61200 {
		return nil, fmt.Errorf("%w: Opus packet too large", ErrOggInvalidPacket)
	}
	samples, err := packetSamples48k(packet)
	if err != nil {
		return nil, err
	}
	if m.granule > math.MaxUint64-uint64(samples) || m.pageSeq == math.MaxUint32 {
		return nil, fmt.Errorf("%w: Ogg counters exhausted", ErrOggInvalidPacket)
	}
	m.granule += uint64(samples)
	m.packets++
	return m.makePage(0, m.granule, packet), nil
}

// Close terminates the Ogg stream with a zero-segment EOS page. This is
// distinct from closing the ASR WebSocket session: send EOS first, then call
// ASR CloseSend. Calling Close repeatedly returns nil, nil. A stream without
// audio packets is rejected.
func (m *OggMuxer) Close() ([]byte, error) {
	if m == nil {
		return nil, ErrOggClosed
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, nil
	}
	if !m.started || m.packets == 0 || m.granule < uint64(m.cfg.PreSkip) {
		return nil, ErrOggNoPackets
	}
	if m.pageSeq == math.MaxUint32 {
		return nil, fmt.Errorf("%w: Ogg page counter exhausted", ErrOggInvalidPacket)
	}
	m.closed = true
	return m.makePage(0x04, m.granule, nil), nil
}

// GranulePosition returns total decoded samples in the Ogg stream measured
// at 48 kHz, irrespective of OpusHead.InputSampleRate.
func (m *OggMuxer) GranulePosition() uint64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.granule
}

func (m *OggMuxer) makePage(flags byte, granule uint64, packet []byte) []byte {
	// A lacing value of 255 continues a packet; a final lacing value below
	// 255 terminates it. Even exact multiples of 255 need a final zero value.
	segments := 0
	if len(packet) != 0 {
		segments = len(packet)/255 + 1
	}
	page := make([]byte, 27+segments+len(packet))
	copy(page[:4], "OggS")
	page[4] = 0
	page[5] = flags
	binary.LittleEndian.PutUint64(page[6:14], granule)
	binary.LittleEndian.PutUint32(page[14:18], m.serial)
	binary.LittleEndian.PutUint32(page[18:22], m.pageSeq)
	page[26] = byte(segments)
	remaining := len(packet)
	for i := 0; i < segments; i++ {
		size := remaining
		if size > 255 {
			size = 255
		}
		page[27+i] = byte(size)
		remaining -= size
	}
	copy(page[27+segments:], packet)
	binary.LittleEndian.PutUint32(page[22:26], oggChecksum(page))
	m.pageSeq++
	return page
}

var oggCRCTable = func() [256]uint32 {
	var table [256]uint32
	for i := range table {
		v := uint32(i) << 24
		for b := 0; b < 8; b++ {
			if v&0x80000000 != 0 {
				v = (v << 1) ^ 0x04c11db7
			} else {
				v <<= 1
			}
		}
		table[i] = v
	}
	return table
}()

func oggChecksum(page []byte) uint32 {
	var crc uint32
	for _, b := range page {
		crc = (crc << 8) ^ oggCRCTable[byte(crc>>24)^b]
	}
	return crc
}
