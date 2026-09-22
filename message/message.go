package message

import (
	"ontology/unknown"
	"ontology/wire"
)

// entry is one field occurrence in original wire order.
type entry struct {
	known bool
	num   int
	// raw holds the complete encoded field for known occurrences, so an
	// unmodified field round-trips byte-exactly even if its original
	// encoding was non-canonical.
	raw []byte
	// uref indexes Message.unknowns for unknown occurrences.
	uref int
}

// Message is a parsed message. It is owned by its caller and is not safe
// for concurrent mutation, but write-back never modifies it, so one
// Message may be marshaled from many goroutines at once.
type Message struct {
	parser   *Parser
	entries  []entry
	unknowns unknown.Set
	values   map[int]any // uint64, []byte or *Message
	dirty    map[int]bool
	deleted  map[int]bool
}

// Uint returns the value of a known varint field.
func (m *Message) Uint(num int) (uint64, bool) {
	v, ok := m.values[num].(uint64)
	return v, ok
}

// Bytes returns a copy of the value of a known bytes field.
func (m *Message) Bytes(num int) ([]byte, bool) {
	v, ok := m.values[num].([]byte)
	if !ok {
		return nil, false
	}
	out := make([]byte, len(v))
	copy(out, v)
	return out, true
}

// Nested returns the value of a known message-typed field.
func (m *Message) Nested(num int) (*Message, bool) {
	v, ok := m.values[num].(*Message)
	return v, ok
}

// SetUint sets a known varint field. Unknown numbers or wrong wire types
// are ignored.
func (m *Message) SetUint(num int, v uint64) {
	if t, ok := m.parser.schema[num]; !ok || t != wire.Varint {
		return
	}
	m.values[num] = v
	m.markDirty(num)
}

// SetBytes sets a known bytes field, copying b.
func (m *Message) SetBytes(num int, b []byte) {
	if t, ok := m.parser.schema[num]; !ok || t != wire.Bytes {
		return
	}
	cp := make([]byte, len(b))
	copy(cp, b)
	m.values[num] = cp
	m.markDirty(num)
}

// SetNested sets a known message-typed field.
func (m *Message) SetNested(num int, n *Message) {
	if t, ok := m.parser.schema[num]; !ok || t != wire.Message || n == nil {
		return
	}
	m.values[num] = n
	m.markDirty(num)
}

// Delete removes a known field. Its occurrences are dropped on
// write-back; all other fields keep their relative order.
func (m *Message) Delete(num int) {
	if _, ok := m.parser.schema[num]; !ok {
		return
	}
	delete(m.values, num)
	delete(m.dirty, num)
	m.deleted[num] = true
}

// Unknowns returns a copy of the unknown fields in wire order.
func (m *Message) Unknowns() []unknown.Field { return m.unknowns.Fields() }

func (m *Message) markDirty(num int) {
	m.dirty[num] = true
	delete(m.deleted, num)
}

// modified reports whether the message or any nested message has been
// changed since parsing.
func (m *Message) modified() bool {
	if len(m.dirty) > 0 || len(m.deleted) > 0 {
		return true
	}
	for _, v := range m.values {
		if sub, ok := v.(*Message); ok && sub.modified() {
			return true
		}
	}
	return false
}
