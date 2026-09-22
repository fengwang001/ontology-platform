package message

// Varint returns the value of the first occurrence of a non-repeated
// varint field and whether it was present.
func (m *Msg) Varint(num uint64) (uint64, bool) {
	i := m.findKnown(num)
	if i < 0 {
		return 0, false
	}
	return m.slots[i].entry.u, true
}

// Bytes returns a copy of the first occurrence of a bytes field.
func (m *Msg) Bytes(num uint64) ([]byte, bool) {
	i := m.findKnown(num)
	if i < 0 {
		return nil, false
	}
	return cloneBytes(m.slots[i].entry.b), true
}

// Message returns the nested message of the first occurrence.
func (m *Msg) Message(num uint64) (*Msg, bool) {
	i := m.findKnown(num)
	if i < 0 {
		return nil, false
	}
	return m.slots[i].entry.m, true
}

// Varints returns every occurrence of a repeated varint field in
// order.
func (m *Msg) Varints(num uint64) []uint64 {
	var out []uint64
	for _, s := range m.slots {
		if s.known && s.entry.num == num {
			out = append(out, s.entry.u)
		}
	}
	return out
}

// SetVarint replaces the value of a non-repeated varint field. The
// field's slot keeps its position; only its payload changes, so
// surrounding unknown fields stay put. It panics if num is not a
// declared non-repeated varint field.
func (m *Msg) SetVarint(num, v uint64) {
	e := m.mustEntry(num, KindVarint, false)
	e.u = v
	e.dirty = true
}

// SetBytes replaces a non-repeated bytes field with a copy of b.
func (m *Msg) SetBytes(num uint64, b []byte) {
	e := m.mustEntry(num, KindBytes, false)
	e.b = cloneBytes(b)
	e.dirty = true
}

// SetMessage replaces a non-repeated nested message field.
func (m *Msg) SetMessage(num uint64, nested *Msg) {
	e := m.mustEntry(num, KindMessage, false)
	e.m = nested
	e.dirty = true
}

// Delete removes every occurrence of the known field num. Unknown
// fields are never affected; remaining slots keep relative order,
// which is precisely where the deleted field's neighbours already
// were. It reports whether a known field was removed.
func (m *Msg) Delete(num uint64) bool { return m.removeKnown(num) }

// mustEntry returns the first entry for a declared non-repeated
// field of the expected kind.
func (m *Msg) mustEntry(num uint64, kind FieldKind, repeated bool) *knownEntry {
	decl, ok := m.schema.lookup(num)
	if !ok || decl.Kind != kind || decl.Repeated != repeated {
		panic("message: field not declared with the requested kind")
	}
	i := m.findKnown(num)
	if i < 0 {
		panic("message: field not present")
	}
	return m.slots[i].entry
}
