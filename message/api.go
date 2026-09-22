package message

import "ontology/unknown"

// ID returns the value of the last occurrence of field 1, or 0 if absent.
func (m *Message) ID() uint64 {
	for i := len(m.order) - 1; i >= 0; i-- {
		if s := m.order[i]; s.kind == slotID {
			return m.ids[s.idx].val
		}
	}
	return 0
}

// SetID replaces all occurrences of field 1 with a single value, kept at
// the position of the first occurrence (appended at the end if absent).
// Unknown fields keep their positions.
func (m *Message) SetID(v uint64) {
	first := m.keepFirst(slotID)
	if first < 0 {
		m.ids = append(m.ids, idOcc{val: v, dirty: true})
		m.order = append(m.order, slot{kind: slotID, idx: len(m.ids) - 1})
		return
	}
	m.ids[first] = idOcc{val: v, dirty: true}
}

// ClearID removes all occurrences of field 1. Unknown fields keep their
// relative order.
func (m *Message) ClearID() {
	m.dropSlots(slotID)
}

// Name returns a copy of the last occurrence of field 2, or nil if absent.
func (m *Message) Name() []byte {
	for i := len(m.order) - 1; i >= 0; i-- {
		if s := m.order[i]; s.kind == slotName {
			return dup(m.names[s.idx].val)
		}
	}
	return nil
}

// SetName replaces all occurrences of field 2 with a single value, kept
// at the position of the first occurrence. The input is copied.
func (m *Message) SetName(b []byte) {
	first := m.keepFirst(slotName)
	if first < 0 {
		m.names = append(m.names, nameOcc{val: dup(b), dirty: true})
		m.order = append(m.order, slot{kind: slotName, idx: len(m.names) - 1})
		return
	}
	m.names[first] = nameOcc{val: dup(b), dirty: true}
}

// ClearName removes all occurrences of field 2.
func (m *Message) ClearName() {
	m.dropSlots(slotName)
}

// Child returns the last occurrence of field 3, or nil if absent. The
// returned message may be edited; the change is reflected on Marshal.
func (m *Message) Child() *Message {
	for i := len(m.order) - 1; i >= 0; i-- {
		if s := m.order[i]; s.kind == slotChild {
			return m.childs[s.idx].child
		}
	}
	return nil
}

// SetChild replaces all occurrences of field 3 with c, kept at the
// position of the first occurrence. Ownership of c transfers to m.
func (m *Message) SetChild(c *Message) {
	if c == nil {
		m.ClearChild()
		return
	}
	first := m.keepFirst(slotChild)
	if first < 0 {
		m.childs = append(m.childs, childOcc{child: c, dirty: true})
		m.order = append(m.order, slot{kind: slotChild, idx: len(m.childs) - 1})
		return
	}
	m.childs[first] = childOcc{child: c, dirty: true}
}

// ClearChild removes all occurrences of field 3.
func (m *Message) ClearChild() {
	m.dropSlots(slotChild)
}

// UnknownFields returns a copy of the unknown fields in wire order.
func (m *Message) UnknownFields() []unknown.Field {
	return m.unk.Fields()
}

// keepFirst removes all slots of kind k except the first and returns the
// occurrence index of that first slot, or -1 if there was none.
func (m *Message) keepFirst(k slotKind) int {
	first := -1
	kept := m.order[:0]
	for _, s := range m.order {
		if s.kind == k {
			if first < 0 {
				first = s.idx
				kept = append(kept, s)
			}
			continue
		}
		kept = append(kept, s)
	}
	m.order = kept
	return first
}

// dropSlots removes all slots of kind k, preserving the order of the rest.
func (m *Message) dropSlots(k slotKind) {
	kept := m.order[:0]
	for _, s := range m.order {
		if s.kind != k {
			kept = append(kept, s)
		}
	}
	m.order = kept
}
