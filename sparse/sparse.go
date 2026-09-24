// Package sparse provides a sparse sequence->offset index over a segment
// file. Every interval-th event becomes an anchor. The index is a pure
// acceleration structure: it stores nothing that cannot be re-derived by
// scanning the segment, so it can be deleted and rebuilt byte-identically.
package sparse

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"ontology/event"
	"ontology/segment"
)

const (
	headerSize = 20 // magic(4) + interval(8) + numAnchors(8)
	entrySize  = 16 // seq(8) + offset(8)
	magic      = "SPX1"
)

var ErrBadIndex = errors.New("sparse: corrupt index file")

// Anchor records that event Seq begins at file byte Offset.
type Anchor struct {
	Seq    uint64
	Offset uint64
}

// Index is the in-memory representation of an index file.
type Index struct {
	Interval uint64
	Anchors  []Anchor
}

// IndexPath maps a segment path to the path of its index file.
func IndexPath(segPath string) string {
	return strings.TrimSuffix(segPath, ".log") + ".idx"
}

// Build derives an index by scanning the segment from its first record.
func Build(segPath string, interval uint64) (*Index, error) {
	if interval == 0 {
		return nil, fmt.Errorf("sparse: interval must be > 0")
	}
	r, err := segment.Open(segPath)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	idx := &Index{Interval: interval}
	offset := uint64(segment.HeaderSize)
	seq := r.Hdr.FirstSeq
	for i := uint64(0); ; i++ {
		_, n, err := event.Decode(r.Section(offset))
		if err == io.EOF {
			if i != r.Hdr.Count {
				return nil, fmt.Errorf("sparse: scanned %d events, header says %d", i, r.Hdr.Count)
			}
			return idx, nil
		}
		if err != nil {
			return nil, err
		}
		if i%interval == 0 {
			idx.Anchors = append(idx.Anchors, Anchor{Seq: seq, Offset: offset})
		}
		offset += uint64(n)
		seq++
	}
}

func (idx *Index) encode() []byte {
	buf := make([]byte, headerSize+entrySize*len(idx.Anchors))
	copy(buf, magic)
	binary.BigEndian.PutUint64(buf[4:], idx.Interval)
	binary.BigEndian.PutUint64(buf[12:], uint64(len(idx.Anchors)))
	for i, a := range idx.Anchors {
		base := headerSize + entrySize*i
		binary.BigEndian.PutUint64(buf[base:], a.Seq)
		binary.BigEndian.PutUint64(buf[base+8:], a.Offset)
	}
	return buf
}

// Save writes the index deterministically to path.
func (idx *Index) Save(path string) error {
	return os.WriteFile(path, idx.encode(), 0o644)
}

// Load parses an index file previously written by Save.
func Load(path string) (*Index, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) < headerSize || string(raw[:4]) != magic {
		return nil, ErrBadIndex
	}
	idx := &Index{
		Interval: binary.BigEndian.Uint64(raw[4:]),
	}
	n := binary.BigEndian.Uint64(raw[12:])
	if uint64(len(raw)) != headerSize+entrySize*n || idx.Interval == 0 {
		return nil, ErrBadIndex
	}
	for i := uint64(0); i < n; i++ {
		base := headerSize + entrySize*i
		idx.Anchors = append(idx.Anchors, Anchor{
			Seq:    binary.BigEndian.Uint64(raw[base:]),
			Offset: binary.BigEndian.Uint64(raw[base+8:]),
		})
	}
	return idx, nil
}

// Floor returns the greatest anchor with Seq <= seq, and whether such an
// anchor exists (false means seq precedes the first anchor).
func (idx *Index) Floor(seq uint64) (Anchor, bool) {
	i := sort.Search(len(idx.Anchors), func(i int) bool {
		return idx.Anchors[i].Seq > seq
	}) - 1
	if i < 0 {
		return Anchor{}, false
	}
	return idx.Anchors[i], true
}
