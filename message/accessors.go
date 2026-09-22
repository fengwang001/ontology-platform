package message

// Accessors for the known fields. Shared semantics:
//
//   - A getter returns the value of the last occurrence of the field
//     (earlier duplicates are still preserved for re-encoding).
//   - A setter rewrites every existing known occurrence in place, so
//     unknown fields keep their positions; if the field is absent, one
//     new occurrence is appended at the end.
//   - A deleter removes every known occurrence of the field; unknown
//     fields (including unknown uses of the same number) are untouched
//     and keep their relative order.

// ID returns the value of the last occurrence of field 1.
func (m *Message) ID() (uint64, bool) {
	for i := len(m.slots) - 1; i >= 0; i-- {
		if s := m.slots[i]; s.num == fieldID && s.kind == slotVarint {
			return s.vint, true
		}
	}
	return 0, false
}

// SetID sets field 1, updating all existing occurrences in place.
func (m *Message) SetID(v uint64) {
	m.setKnown(fieldID, slotVarint, func(s *slot) { s.vint = v })
}

// DeleteID removes every known occurrence of field 1.
func (m *Message) DeleteID() { m.deleteKnown(fieldID) }

// Name returns the value of the last occurrence of field 2.
func (m *Message) Name() (string, bool) {
	for i := len(m.slots) - 1; i >= 0; i-- {
		if s := m.slots[i]; s.num == fieldName && s.kind == slotBytes {
			return string(s.data), true
		}
	}
	return "", false
}

// SetName sets field 2, updating all existing occurrences in place.
func (m *Message) SetName(v string) {
	m.setKnown(fieldName, slotBytes, func(s *slot) { s.data = []byte(v) })
}

// DeleteName removes every known occurrence of field 2.
func (m *Message) DeleteName() { m.deleteKnown(fieldName) }

// Child returns the value of the last occurrence of field 3.
func (m *Message) Child() (*Message, bool) {
	for i := len(m.slots) - 1; i >= 0; i-- {
		if s := m.slots[i]; s.num == fieldChild && s.kind == slotMessage {
			return s.child, true
		}
	}
	return nil, false
}

// SetChild sets field 3, updating all existing occurrences in place.
func (m *Message) SetChild(c *Message) {
	m.setKnown(fieldChild, slotMessage, func(s *slot) { s.child = c })
}

// DeleteChild removes every known occurrence of field 3.
func (m *Message) DeleteChild() { m.deleteKnown(fieldChild) }

// setKnown applies set to every known occurrence of num, or appends a
// new occurrence at the end if the field is absent.
func (m *Message) setKnown(num uint32, kind slotKind, set func(*slot)) {
	found := false
	for i := range m.slots {
		if m.slots[i].num == num && m.slots[i].kind == kind {
			set(&m.slots[i])
			found = true
		}
	}
	if !found {
		s := slot{num: num, kind: kind}
		set(&s)
		m.slots = append(m.slots, s)
	}
}

// deleteKnown removes every known occurrence of num, preserving the
// order of everything else (unknown fields in particular).
func (m *Message) deleteKnown(num uint32) {
	kept := m.slots[:0]
	for _, s := range m.slots {
		if s.num == num && s.kind != slotUnknown {
			continue
		}
		kept = append(kept, s)
	}
	m.slots = kept
}
