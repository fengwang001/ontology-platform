package message

import "ontology/wire"

// Marshal re-encodes the message. Fields that were never modified are
// re-emitted from their original raw bytes; modified fields are
// re-encoded canonically at their original position. Marshal does not
// mutate m, so marshaling the same message twice yields identical bytes.
func (m *Message) Marshal() []byte {
	var out []byte
	for _, s := range m.order {
		switch s.kind {
		case slotID:
			if o := &m.ids[s.idx]; o.dirty {
				out = wire.AppendVarintField(out, fieldID, o.val)
			} else {
				out = append(out, o.raw...)
			}
		case slotName:
			if o := &m.names[s.idx]; o.dirty {
				out = wire.AppendField(out, fieldName, wire.Bytes, o.val)
			} else {
				out = append(out, o.raw...)
			}
		case slotChild:
			o := &m.childs[s.idx]
			if o.dirty || !o.child.clean() {
				out = wire.AppendField(out, fieldChild, wire.Message, o.child.Marshal())
			} else {
				out = append(out, o.raw...)
			}
		case slotUnknown:
			out = append(out, m.unk.At(s.idx).Raw...)
		}
	}
	return out
}

// clean reports whether the message and all of its nested children are
// unmodified, meaning their stored raw bytes are still authoritative.
func (m *Message) clean() bool {
	for i := range m.ids {
		if m.ids[i].dirty {
			return false
		}
	}
	for i := range m.names {
		if m.names[i].dirty {
			return false
		}
	}
	for i := range m.childs {
		if m.childs[i].dirty || !m.childs[i].child.clean() {
			return false
		}
	}
	return true
}
