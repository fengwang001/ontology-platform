// Package posting defines inverted posting lists: document IDs in
// ascending order, each with ascending positions, plus delta encoding.
package posting

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Entry holds one document and the positions of a term inside it.
type Entry struct {
	Doc uint32
	Pos []uint32
}

// List is a posting list: entries sorted by Doc, positions sorted inside.
type List []Entry

// ErrCorrupt reports malformed encoded posting data.
var ErrCorrupt = errors.New("posting: corrupt data")

// Builder accumulates entries in order. Duplicate (doc, pos) pairs are
// rejected and counted.
type Builder struct {
	list List
	dups int
}

// Add appends (doc, pos). Docs must arrive in non-decreasing order and
// positions in increasing order within a doc. It returns false (and
// counts a duplicate) when (doc, pos) was already added.
func (b *Builder) Add(doc, pos uint32) bool {
	if n := len(b.list); n > 0 {
		last := &b.list[n-1]
		if doc == last.Doc {
			if len(last.Pos) > 0 && pos <= last.Pos[len(last.Pos)-1] {
				b.dups++
				return false
			}
			last.Pos = append(last.Pos, pos)
			return true
		}
		if doc < last.Doc {
			panic("posting: docs must be added in ascending order")
		}
	}
	b.list = append(b.list, Entry{Doc: doc, Pos: []uint32{pos}})
	return true
}

// Dups returns the number of rejected duplicate (doc, pos) additions.
func (b *Builder) Dups() int { return b.dups }

// List returns the accumulated posting list.
func (b *Builder) List() List { return b.list }

// Docs returns the document IDs appearing in the list, ascending.
func (l List) Docs() []uint32 {
	docs := make([]uint32, len(l))
	for i, e := range l {
		docs[i] = e.Doc
	}
	return docs
}

// Encode delta-encodes the list: doc deltas and position deltas are
// stored as uvarints, each preceded by the per-doc position count.
func Encode(l List) []byte {
	var out []byte
	var buf [binary.MaxVarintLen64]byte
	put := func(v uint64) {
		n := binary.PutUvarint(buf[:], v)
		out = append(out, buf[:n]...)
	}
	prevDoc := int64(-1)
	for _, e := range l {
		put(uint64(int64(e.Doc) - prevDoc))
		prevDoc = int64(e.Doc)
		put(uint64(len(e.Pos)))
		prevPos := int64(-1)
		for _, p := range e.Pos {
			put(uint64(int64(p) - prevPos))
			prevPos = int64(p)
		}
	}
	return out
}

// Decode reverses Encode. Any inconsistency yields ErrCorrupt.
func Decode(data []byte) (List, error) {
	var list List
	prevDoc := int64(-1)
	for len(data) > 0 {
		d, n := binary.Uvarint(data)
		if n <= 0 || d == 0 {
			return nil, fmt.Errorf("%w: bad doc delta", ErrCorrupt)
		}
		data = data[n:]
		doc := prevDoc + int64(d)
		prevDoc = doc
		cnt, n := binary.Uvarint(data)
		if n <= 0 || cnt == 0 {
			return nil, fmt.Errorf("%w: bad position count", ErrCorrupt)
		}
		data = data[n:]
		e := Entry{Doc: uint32(doc), Pos: make([]uint32, 0, cnt)}
		prevPos := int64(-1)
		for i := uint64(0); i < cnt; i++ {
			d, n := binary.Uvarint(data)
			if n <= 0 || d == 0 {
				return nil, fmt.Errorf("%w: bad position delta", ErrCorrupt)
			}
			data = data[n:]
			prevPos += int64(d)
			e.Pos = append(e.Pos, uint32(prevPos))
		}
		list = append(list, e)
	}
	return list, nil
}
