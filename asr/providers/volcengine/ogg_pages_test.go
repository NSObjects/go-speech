package volcengine

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"testing"
)

// oggPage is a complete, consecutive Ogg page from one logical Opus stream.
// Opus granule positions use a 48kHz timeline even when the input is 16kHz.
type oggPage struct {
	data    []byte
	granule uint64
}

// Split pages for transport without assuming a constant encoded byte rate.
// Sending arbitrary raw Opus packets as format=ogg is not valid.
func splitOggPages(data []byte) ([]oggPage, error) {
	var pages []oggPage
	for offset := 0; offset < len(data); {
		if len(data)-offset < 27 || !bytes.Equal(data[offset:offset+4], []byte("OggS")) ||
			data[offset+4] != 0 {
			return nil, fmt.Errorf("invalid Ogg page header at byte %d", offset)
		}
		segments := int(data[offset+26])
		if len(data)-offset < 27+segments {
			return nil, fmt.Errorf("truncated Ogg segment table at byte %d", offset)
		}
		size := 27 + segments
		for _, segmentLen := range data[offset+27 : offset+27+segments] {
			size += int(segmentLen)
		}
		if len(data)-offset < size {
			return nil, fmt.Errorf("truncated Ogg payload at byte %d", offset)
		}
		pages = append(pages, oggPage{
			data:    data[offset : offset+size],
			granule: binary.LittleEndian.Uint64(data[offset+6 : offset+14]),
		})
		offset += size
	}
	if len(pages) < 3 || !bytes.Contains(pages[0].data, []byte("OpusHead")) {
		return nil, errors.New("Ogg Opus stream is missing codec headers or audio pages")
	}
	return pages, nil
}

func syntheticOggPage(data []byte, granule uint64) []byte {
	if len(data) > 255 {
		panic("test Ogg page payload exceeds single lacing segment")
	}
	page := make([]byte, 28+len(data))
	copy(page[:4], "OggS")
	// Ogg bitstream version and one lacing segment.
	page[4] = 0
	page[26] = 1
	page[27] = byte(len(data))
	binary.LittleEndian.PutUint64(page[6:14], granule)
	copy(page[28:], data)
	return page
}

func TestSplitOggPagesPreservesBoundaries(t *testing.T) {
	header := syntheticOggPage([]byte("OpusHead dummy"), 0)
	tags := syntheticOggPage([]byte("OpusTags"), 0)
	audio := syntheticOggPage([]byte{0x80, 0x01, 0x02}, 9600)
	original := append(append(append([]byte{}, header...), tags...), audio...)
	pages, err := splitOggPages(original)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 3 {
		t.Fatalf("expected three complete Ogg pages, got %d", len(pages))
	}
	for i, want := range [][]byte{header, tags, audio} {
		if !bytes.Equal(pages[i].data, want) {
			t.Fatalf("page %d does not preserve exact bytes", i)
		}
	}
	if pages[2].granule != 9600 {
		t.Fatalf("Opus granule timeline changed: got %d", pages[2].granule)
	}
}

func TestSplitOggPagesRejectsIncompleteInput(t *testing.T) {
	header := syntheticOggPage([]byte("OpusHead dummy"), 0)
	tags := syntheticOggPage([]byte("OpusTags"), 0)
	audio := syntheticOggPage([]byte{0x80, 0x01, 0x02}, 9600)
	good := append(append(append([]byte{}, header...), tags...), audio...)
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{name: "empty"},
		{name: "truncated header", data: good[:26]},
		{name: "bad magic", data: append([]byte("NotS"), good[4:]...)},
		{name: "truncated segment table", data: good[:27]},
		{name: "truncated payload", data: good[:len(good)-1]},
		{name: "trailing junk", data: append(append([]byte{}, good...), 0xff)},
		{name: "no OpusHead", data: append(append([]byte{}, tags...), tags...)},
		{name: "not enough pages", data: header},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := splitOggPages(tc.data); err == nil {
				t.Fatal("invalid Ogg stream was accepted")
			}
		})
	}
}
