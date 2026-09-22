package message

import "ontology/wire"

// Marshal encodes the message. It does not modify m, so marshalling
// the same message twice yields identical bytes.
func (m *Msg) Marshal() []byte { return m.AppendTo(nil) }

// AppendTo appends the encoding of m to dst and returns the result.
// Fields are written in their recorded encounter order; unknown
// fields are emitted from their preserved raw bytes.
func (m *Msg) AppendTo(dst []byte) []byte {
	for _, e := range m.entries {
		if !e.known {
			dst = append(dst, m.unk.At(e.unkIndex).Raw...)
			continue
		}
		dst = wire.AppendHeader(dst, e.num, e.typ)
		switch e.typ {
		case wire.Varint:
			dst = wire.AppendVarint(dst, e.u64)
		case wire.Bytes:
			dst = wire.AppendVarint(dst, uint64(len(e.data)))
			dst = append(dst, e.data...)
		case wire.Message:
			var payload []byte
			if e.child != nil {
				payload = e.child.Marshal()
			}
			dst = wire.AppendVarint(dst, uint64(len(payload)))
			dst = append(dst, payload...)
		}
	}
	return dst
}
