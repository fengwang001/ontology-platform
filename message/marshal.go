package message

import (
	"ontology/wire"
	"sort"
)

// Marshal returns the wire encoding of m. It never modifies m, so
// marshaling the same Message twice yields identical bytes, and a
// Message returned by Parse may be marshaled concurrently.
func (m *Message) Marshal() []byte { return m.AppendTo(nil) }

// AppendTo appends the wire encoding of m to dst.
//
// Fields are emitted in original wire order. Unknown fields always write
// back their preserved raw bytes. Known fields write back their raw
// bytes too unless modified: a modified field is re-encoded once, at the
// position of its first occurrence; a deleted field is dropped. Known
// fields set on a fresh or parsed Message that never had an occurrence
// are appended at the end, ordered by field number.
func (m *Message) AppendTo(dst []byte) []byte {
	emitted := map[int]bool{}
	for _, e := range m.entries {
		if !e.known {
			dst = m.unknowns.AppendFieldTo(dst, e.uref)
			continue
		}
		if m.deleted[e.num] {
			continue
		}
		if m.fieldDirty(e.num) {
			if emitted[e.num] {
				continue
			}
			emitted[e.num] = true
			dst = m.appendValue(dst, e.num)
			continue
		}
		dst = append(dst, e.raw...)
	}
	var added []int
	for num := range m.dirty {
		if !emitted[num] {
			added = append(added, num)
		}
	}
	sort.Ints(added)
	for _, num := range added {
		dst = m.appendValue(dst, num)
	}
	return dst
}

// fieldDirty reports whether the current value of a known field differs
// from what was parsed, including changes inside nested messages.
func (m *Message) fieldDirty(num int) bool {
	if m.dirty[num] {
		return true
	}
	if sub, ok := m.values[num].(*Message); ok {
		return sub.modified()
	}
	return false
}

// appendValue re-encodes the current value of a known field.
func (m *Message) appendValue(dst []byte, num int) []byte {
	v, ok := m.values[num]
	if !ok {
		return dst
	}
	switch m.parser.schema[num] {
	case wire.Varint:
		u, _ := v.(uint64)
		return wire.AppendVarintField(dst, uint64(num), u)
	case wire.Bytes:
		b, _ := v.([]byte)
		return wire.AppendField(dst, uint64(num), wire.Bytes, b)
	case wire.Message:
		sub, _ := v.(*Message)
		if sub == nil {
			return dst
		}
		dst = wire.AppendHeader(dst, uint64(num), wire.Message)
		payload := sub.AppendTo(nil)
		dst = wire.AppendVarint(dst, uint64(len(payload)))
		return append(dst, payload...)
	}
	return dst
}
