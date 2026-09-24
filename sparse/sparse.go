// Package sparse implements the "sequence -> byte offset" anchor index.
// The index is fully derivable from its segment: anchors land every N events
// at the base event and frame boundaries discovered while scanning.
package sparse

import (
	"encoding/binary"
	"errors"
	"os"
)

// HeaderSize and EntrySize are the fixed on-disk sizes in bytes.
const (
	HeaderSize = 16
	EntrySize  = 16
)

var indexMagic = [8]byte{'O', 'N', 'L', 'O', 'G', 'I', 'D', '1'}

// ErrBadIndex means an index file fails magic/size validation.
var ErrBadIndex = errors.New("sparse: bad index file")

// Anchor maps one event sequence to its frame offset inside the segment.
type Anchor struct {
	Seq    uint64
	Offset int64
}

// Index is an in-memory sparse index.
type Index struct {
	Every   uint64
	Anchors []Anchor
}

// ShouldAnchor reports whether the event at zero-based ordinal i gets an
// anchor. Anchors land at ordinals N, 2N, ... so the first anchor is base+N;
// locations before it are reached by a bounded prefix scan (< N skips).
func ShouldAnchor(every uint64, i uint64) bool {
	return every > 0 && i > 0 && i%every == 0
}

// Encode serializes the index deterministically.
func (x *Index) Encode() []byte {
	b := make([]byte, HeaderSize+EntrySize*len(x.Anchors))
	copy(b, indexMagic[:])
	binary.BigEndian.PutUint64(b[8:], x.Every)
	for i, a := range x.Anchors {
		o := HeaderSize + i*EntrySize
		binary.BigEndian.PutUint64(b[o:], a.Seq)
		binary.BigEndian.PutUint64(b[o+8:], uint64(a.Offset))
	}
	return b
}

// Decode parses an index byte slice.
func Decode(b []byte) (*Index, error) {
	if len(b) < HeaderSize || (len(b)-HeaderSize)%EntrySize != 0 {
		return nil, ErrBadIndex
	}
	var magic [8]byte
	copy(magic[:], b)
	if magic != indexMagic {
		return nil, ErrBadIndex
	}
	every := binary.BigEndian.Uint64(b[8:])
	n := (len(b) - HeaderSize) / EntrySize
	x := &Index{Every: every, Anchors: make([]Anchor, n)}
	for i := 0; i < n; i++ {
		o := HeaderSize + i*EntrySize
		x.Anchors[i] = Anchor{
			Seq:    binary.BigEndian.Uint64(b[o:]),
			Offset: int64(binary.BigEndian.Uint64(b[o+8:])),
		}
	}
	return x, nil
}

// WriteFile atomically replaces path with the encoded index.
func (x *Index) WriteFile(path string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, x.Encode(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ReadFile loads an index file; a missing file returns io.EOF-compatible
// nil index and nil error (callers rebuild from the segment).
func ReadFile(path string) (*Index, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return Decode(b)
}

// Lookup returns the greatest anchor with Seq <= seq and its position.
// If no such anchor exists it returns false (caller scans from segment start).
func (x *Index) Lookup(seq uint64) (Anchor, bool) {
	lo, hi := 0, len(x.Anchors)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if x.Anchors[mid].Seq <= seq {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return Anchor{}, false
	}
	return x.Anchors[lo-1], true
}
