package message

import "ontology/wire"

// Marshal serialises the message. It never mutates the receiver:
// untouched fields use their captured raw payload and modified
// fields are encoded into a fresh buffer, so repeated calls on the
// same Msg (even concurrently) yield identical bytes.
//
// Ordering: slots are emitted in their stored sequence, which is the
// original input sequence minus deleted fields, i.e. the surviving
// known and unknown fields retain their exact relative order. That is
// the only ordering consistent with byte-for-byte round trip when
// unknown fields may be interleaved with known ones.
func (m *Msg) Marshal() ([]byte, error) {
	return m.appendMarshal(nil)
}

func (m *Msg) appendMarshal(dst []byte) ([]byte, error) {
	for _, s := range m.slots {
		if !s.known {
			dst = m.unk.At(s.uIdx).Append(dst)
			continue
		}
		var err error
		if dst, err = s.entry.appendMarshal(dst); err != nil {
			return nil, err
		}
	}
	return dst, nil
}

func (e *knownEntry) appendMarshal(dst []byte) ([]byte, error) {
	if !e.dirty {
		dst = wire.AppendHeader(dst, e.num, e.wt, len(e.raw))
		return append(dst, e.raw...), nil
	}
	switch e.wt {
	case wire.Varint:
		dst = wire.AppendHeader(dst, e.num, e.wt, 0)
		dst = wire.AppendVarint(dst, e.u)
	case wire.Bytes:
		dst = wire.AppendHeader(dst, e.num, e.wt, len(e.b))
		dst = append(dst, e.b...)
	case wire.Message:
		body, err := e.m.Marshal()
		if err != nil {
			return nil, err
		}
		dst = wire.AppendHeader(dst, e.num, e.wt, len(body))
		dst = append(dst, body...)
	}
	return dst, nil
}
