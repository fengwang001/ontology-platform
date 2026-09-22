package message

import "ontology/unknown"

// slot is one position in the merged input sequence. Exactly one of
// the two representations is meaningful: a parsed known value, or an
// index into Msg.unk for an unknown field.
type slot struct {
	known bool
	entry *knownEntry
	uIdx  int
}

// knownEntry is a parsed known field. raw is the verbatim payload
// captured at parse time and is used until the value is modified;
// once dirty, the typed value is re-encoded instead.
type knownEntry struct {
	num   uint64
	wt    byte
	raw   []byte
	dirty bool
	u     uint64
	b     []byte
	m     *Msg
}

// Msg is a parsed message. Values are owned exclusively by the Msg;
// Marshal never mutates it, so the same Msg may be marshalled
// repeatedly and concurrently.
type Msg struct {
	schema *Schema
	slots  []slot
	unk    unknown.Fields
}

// New returns an empty message using schema (which may be nil).
func New(schema *Schema) *Msg {
	return &Msg{schema: schema}
}

// Unknowns returns the retained unknown fields in their relative
// order among themselves. The returned container is owned by the
// caller and is a snapshot copy.
func (m *Msg) Unknowns() *unknown.Fields {
	out := &unknown.Fields{}
	for i := range m.unk.All() {
		_ = out.Add(m.unk.At(i), -1)
	}
	return out
}

// findKnown returns the index of the first known slot with number n.
func (m *Msg) findKnown(n uint64) int {
	for i := range m.slots {
		if m.slots[i].known && m.slots[i].entry.num == n {
			return i
		}
	}
	return -1
}

// removeKnown deletes every known slot with number n and reports
// whether anything was removed. Unknown fields (which by definition
// carry no declared number) are untouched, so the surviving slots
// stay in the exact relative order required by round trip.
func (m *Msg) removeKnown(n uint64) bool {
	kept := m.slots[:0]
	removed := false
	for _, s := range m.slots {
		if s.known && s.entry.num == n {
			removed = true
			continue
		}
		kept = append(kept, s)
	}
	m.slots = kept
	return removed
}
