package volcengine

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"testing"

	"github.com/NSObjects/go-speech/audio/codec/opus"
)

// Source frames are extracted solely to simulate a device's packet output.
// This test helper is not a production demuxer or a generic RTP parser.
type fixtureRawOpus struct {
	packets [][]byte
	config opus.OggConfig
}

func extractFixtureRawOpus(data []byte) (fixtureRawOpus, error) {
	pages, err := splitOggPages(data)
	if err != nil { return fixtureRawOpus{}, err }
	var result fixtureRawOpus
	var pending []byte
	var serial, seq uint32
	packetIndex := 0
	for i, p := range pages {
		raw := p.data
		s := binary.LittleEndian.Uint32(raw[14:18])
		n := binary.LittleEndian.Uint32(raw[18:22])
		if i == 0 {
			serial, seq = s, n
		} else {
			if s != serial || n != seq+1 { return fixtureRawOpus{}, fmt.Errorf("Ogg stream sequence broken at page %d", i) }
			seq = n
		}
		if (raw[5]&1 != 0) != (len(pending) != 0) {
			return fixtureRawOpus{}, fmt.Errorf("bad Ogg continuation at page %d", i)
		}
		segments := int(raw[26])
		laces := raw[27:27+segments]
		payload := raw[27+segments:]
		at := 0
		for _, size := range laces {
			pending = append(pending, payload[at:at+int(size)]...)
			at += int(size)
			if size == 255 { continue }
			switch packetIndex {
			case 0:
				if len(pending) < 19 || !bytes.HasPrefix(pending, []byte("OpusHead")) {
					return fixtureRawOpus{}, errors.New("missing OpusHead")
				}
				result.config = opus.OggConfig{
					Channels: int(pending[9]),
					PreSkip: binary.LittleEndian.Uint16(pending[10:12]),
					InputSampleRate: int(binary.LittleEndian.Uint32(pending[12:16])),
				}
			case 1:
				if !bytes.HasPrefix(pending, []byte("OpusTags")) { return fixtureRawOpus{}, errors.New("missing OpusTags") }
			default:
				if len(pending) > 0 { result.packets = append(result.packets, append([]byte(nil), pending...)) }
			}
			packetIndex++
			pending = nil
		}
	}
	if len(pending) != 0 || len(result.packets) == 0 { return fixtureRawOpus{}, errors.New("incomplete Ogg packet or no audio packets") }
	if result.config.Channels != 1 || result.config.InputSampleRate != 16000 {
		return fixtureRawOpus{}, fmt.Errorf("unexpected fixture rate/channels: %+v", result.config)
	}
	return result, nil
}

func TestExtractFixtureRawOpusRoundTrip(t *testing.T) {
	m, err := opus.NewOggMuxer(opus.OggConfig{Channels: 1, InputSampleRate: 16000, PreSkip: 312})
	if err != nil { t.Fatal(err) }
	ogg, err := m.Headers()
	if err != nil { t.Fatal(err) }
	packets := [][]byte{{0x98, 0x01, 0x02}, {0x98, 0x03, 0x04}}
	for _, p := range packets {
		page, err := m.WritePacket(p)
		if err != nil { t.Fatal(err) }
		ogg = append(ogg, page...)
	}
	end, err := m.Close()
	if err != nil { t.Fatal(err) }
	ogg = append(ogg, end...)
	got, err := extractFixtureRawOpus(ogg)
	if err != nil { t.Fatal(err) }
	if got.config.PreSkip != 312 || len(got.packets) != len(packets) { t.Fatalf("unexpected remux result: %+v", got) }
	for i, p := range packets {
		if !bytes.Equal(p, got.packets[i]) { t.Fatalf("Opus packet %d changed", i) }
	}
}

func TestExtractFixtureRawOpusRejectsSequenceGap(t *testing.T) {
	m, err := opus.NewOggMuxer(opus.OggConfig{Channels: 1, InputSampleRate: 16000})
	if err != nil { t.Fatal(err) }
	ogg, err := m.Headers()
	if err != nil { t.Fatal(err) }
	p, err := m.WritePacket([]byte{0x98, 0x01, 0x02})
	if err != nil { t.Fatal(err) }
	binary.LittleEndian.PutUint32(p[18:22], 1) // duplicates OpusTags page number
	ogg = append(ogg, p...)
	eos, err := m.Close()
	if err != nil { t.Fatal(err) }
	ogg = append(ogg, eos...)
	if _, err := extractFixtureRawOpus(ogg); err == nil { t.Fatal("duplicate page sequence was accepted") }
}
