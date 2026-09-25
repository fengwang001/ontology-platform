// Package sparse implements a sparse offset index over a segment:
// every N events one anchor maps a sequence number to a byte offset.
package sparse

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sort"

	"ontology/segment"
)

// ErrBadIndex is returned when an index file cannot be decoded.
var ErrBadIndex = errors.New("sparse: malformed index file")

const idxMagic = "OIDX"

// Anchor maps a sequence number to the byte offset of its record.
type Anchor struct {
	Seq    uint64
	Offset int64
}

// Index is a sparse sequence-to-offset index for one segment.
type Index struct {
	Every   uint64
	Anchors []Anchor
}

// Build derives the index purely from the segment file, so a deleted
// index can always be rebuilt byte-identically.
func Build(segPath string) (Index, error) {
	f, err := os.Open(segPath)
	if err != nil {
		return Index{}, err
	}
	defer f.Close()
	hdr, err := segment.ReadHeader(f)
	if err != nil {
		return Index{}, err
	}
	idx := Index{Every: hdr.IndexEvery}
	off := int64(segment.HeaderSize)
	for k := uint64(0); k < hdr.Count; k++ {
		if k%hdr.IndexEvery == 0 {
			idx.Anchors = append(idx.Anchors, Anchor{Seq: hdr.FirstSeq + k, Offset: off})
		}
		_, next, err := segment.ReadRecordAt(f, off)
		if err != nil {
			return Index{}, err
		}
		off = next
	}
	return idx, nil
}

// Locate returns the greatest anchor with Seq <= from. The second
// return value is false when from is below the first anchor.
func (idx Index) Locate(from uint64) (Anchor, bool) {
	i := sort.Search(len(idx.Anchors), func(i int) bool {
		return idx.Anchors[i].Seq > from
	}) - 1
	if i < 0 {
		return Anchor{}, false
	}
	return idx.Anchors[i], true
}

// Encode serializes the index deterministically.
func (idx Index) Encode() []byte {
	buf := make([]byte, 20+16*len(idx.Anchors))
	copy(buf, idxMagic)
	binary.BigEndian.PutUint64(buf[4:], idx.Every)
	binary.BigEndian.PutUint64(buf[12:], uint64(len(idx.Anchors)))
	for i, a := range idx.Anchors {
		binary.BigEndian.PutUint64(buf[20+16*i:], a.Seq)
		binary.BigEndian.PutUint64(buf[28+16*i:], uint64(a.Offset))
	}
	return buf
}

// Decode parses bytes produced by Encode.
func Decode(buf []byte) (Index, error) {
	if len(buf) < 20 || string(buf[:4]) != idxMagic {
		return Index{}, ErrBadIndex
	}
	n := binary.BigEndian.Uint64(buf[12:])
	if len(buf) != 20+16*int(n) {
		return Index{}, fmt.Errorf("%w: size %d for %d anchors", ErrBadIndex, len(buf), n)
	}
	idx := Index{Every: binary.BigEndian.Uint64(buf[4:])}
	for i := 0; i < int(n); i++ {
		idx.Anchors = append(idx.Anchors, Anchor{
			Seq:    binary.BigEndian.Uint64(buf[20+16*i:]),
			Offset: int64(binary.BigEndian.Uint64(buf[28+16*i:])),
		})
	}
	return idx, nil
}

// WriteFile writes the index to path.
func WriteFile(path string, idx Index) error {
	return os.WriteFile(path, idx.Encode(), 0o644)
}

// ReadFile reads an index from path.
func ReadFile(path string) (Index, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return Index{}, err
	}
	return Decode(buf)
}
