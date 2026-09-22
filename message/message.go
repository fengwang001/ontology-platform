// Package message parses and serialises the known message structure
// on top of the wire and unknown packages.
//
// Known schema (this version):
//
//	field 1: id     varint
//	field 2: name   bytes
//	field 3: child  message (nested Msg)
//
// Any other field — including a known field number arriving with an
// unexpected wire type — is treated as unknown and preserved.
//
// Ordering rule, derived from the byte-identity invariant: since a
// parse followed by a write must reproduce the input byte-for-byte,
// and known and unknown fields may be arbitrarily interleaved in the
// input, the only possible write order is the exact encounter order
// of the fields as parsed. Msg therefore records one ordered entry
// per field occurrence (known or unknown) and writes them back in
// that order. Editing a known field replaces its occurrences in
// place (at the first occurrence's position); deleting one removes
// only its own entries. Unknown entries are never reordered, so
// their relative order — and their position relative to surviving
// known fields — is always preserved.
package message

import (
	"ontology/unknown"
	"ontology/wire"
)

// Field numbers of the known schema.
const (
	fieldID    = 1
	fieldName  = 2
	fieldChild = 3
)

// Msg is a decoded message. The zero value is an empty message and
// marshals to zero bytes.
type Msg struct {
	entries []entry
	unk     unknown.Set
}

// entry is one field occurrence, in encounter order.
type entry struct {
	known    bool
	num      uint64
	typ      wire.Type
	u64      uint64   // known varint payload
	data     []byte   // known bytes payload (owned copy)
	child    *Msg     // known message payload
	unkIndex int      // index into unk, valid when !known
}

// ID returns the last occurrence of field 1, or 0 if absent.
func (m *Msg) ID() uint64 {
	var v uint64
	for _, e := range m.entries {
		if e.known && e.num == fieldID {
			v = e.u64
		}
	}
	return v
}

// SetID replaces all occurrences of field 1 with a single one, kept
// at the first occurrence's position (appended if none existed).
func (m *Msg) SetID(v uint64) {
	m.setKnown(entry{known: true, num: fieldID, typ: wire.Varint, u64: v})
}

// ClearID removes all occurrences of field 1.
func (m *Msg) ClearID() { m.clear(fieldID) }

// Name returns the last occurrence of field 2, or "" if absent.
func (m *Msg) Name() string {
	var s string
	for _, e := range m.entries {
		if e.known && e.num == fieldName {
			s = string(e.data)
		}
	}
	return s
}

// SetName replaces all occurrences of field 2 with a single one.
func (m *Msg) SetName(s string) {
	m.setKnown(entry{known: true, num: fieldName, typ: wire.Bytes, data: []byte(s)})
}

// ClearName removes all occurrences of field 2.
func (m *Msg) ClearName() { m.clear(fieldName) }

// Child returns the last occurrence of field 3, or nil if absent.
func (m *Msg) Child() *Msg {
	var c *Msg
	for _, e := range m.entries {
		if e.known && e.num == fieldChild {
			c = e.child
		}
	}
	return c
}

// SetChild replaces all occurrences of field 3 with a single one.
// A nil child clears the field.
func (m *Msg) SetChild(c *Msg) {
	if c == nil {
		m.clear(fieldChild)
		return
	}
	m.setKnown(entry{known: true, num: fieldChild, typ: wire.Message, child: c})
}

// ClearChild removes all occurrences of field 3.
func (m *Msg) ClearChild() { m.clear(fieldChild) }

// Unknowns returns the unknown fields of this message level, in
// their original order.
func (m *Msg) Unknowns() []unknown.Field { return m.unk.Fields() }

// setKnown replaces every occurrence of ne.num with the single
// entry ne, positioned at the first previous occurrence.
func (m *Msg) setKnown(ne entry) {
	out := make([]entry, 0, len(m.entries)+1)
	placed := false
	for _, e := range m.entries {
		if e.known && e.num == ne.num {
			if !placed {
				out = append(out, ne)
				placed = true
			}
			continue
		}
		out = append(out, e)
	}
	if !placed {
		out = append(out, ne)
	}
	m.entries = out
}

// clear removes every known occurrence of num.
func (m *Msg) clear(num uint64) {
	out := make([]entry, 0, len(m.entries))
	for _, e := range m.entries {
		if e.known && e.num == num {
			continue
		}
		out = append(out, e)
	}
	m.entries = out
}
