// Package sparse provides the (seq, frame-offset) anchor index for one segment.
// Every Nth frame is an anchor; the index is a pure cache derivable byte for
// byte from the segment file alone.
package sparse

import (
	"encoding/binary"
	"errors"
	"os"
)

const (
	idxMagic = "ONTOIDX1"
	EntryLen = 16
)

var (
	ErrBadIndexMagic = errors.New("sparse: bad index magic")
	ErrShortIndex    = errors.New("sparse: truncated index")
)

// Entry is one anchor: event Seq is at frame-region offset Off.
type Entry struct {
	Seq uint64
	Off uint64
}

// Index is the ordered anchor list for one segment.
type Index struct {
	Every   int
	Entries []Entry
}

// Add records an anchor for the i-th (0-based) frame at offset off.
func (x *Index) Add(i int, seq, off uint64) {
	if x.Every <= 0 {
		x.Every = 1
	}
	if i%x.Every == 0 {
		x.Entries = append(x.Entries, Entry{Seq: seq, Off: off})
	}
}

// Lookup returns the greatest anchor with Seq <= from and whether one exists.
// Anchors above from must never be selected: scanning forward from such an
// anchor would miss events between the previous anchor and it.
func (x *Index) Lookup(from uint64) (Entry, bool) {
	lo, hi := 0, len(x.Entries)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if x.Entries[mid].Seq <= from {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return Entry{}, false
	}
	return x.Entries[lo-1], true
}

// Marshal renders the exact on-disk bytes.
func (x *Index) Marshal() []byte {
	out := make([]byte, len(idxMagic)+len(x.Entries)*EntryLen)
	copy(out[:8], idxMagic)
	for i, e := range x.Entries {
		base := 8 + i*EntryLen
		binary.BigEndian.PutUint64(out[base:base+8], e.Seq)
		binary.BigEndian.PutUint64(out[base+8:base+16], e.Off)
	}
	return out
}

// Unmarshal parses index bytes.
func Unmarshal(raw []byte) (*Index, error) {
	if len(raw) < len(idxMagic) {
		return nil, ErrShortIndex
	}
	if string(raw[:8]) != idxMagic {
		return nil, ErrBadIndexMagic
	}
	body := raw[8:]
	if len(body)%EntryLen != 0 {
		return nil, ErrShortIndex
	}
	x := &Index{}
	for len(body) > 0 {
		x.Entries = append(x.Entries, Entry{
			Seq: binary.BigEndian.Uint64(body[:8]),
			Off: binary.BigEndian.Uint64(body[8:16]),
		})
		body = body[EntryLen:]
	}
	return x, nil
}

// Save writes the index to path.
func (x *Index) Save(path string) error {
	return os.WriteFile(path, x.Marshal(), 0o644)
}

// Load reads an index from path.
func Load(path string) (*Index, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Unmarshal(raw)
}
