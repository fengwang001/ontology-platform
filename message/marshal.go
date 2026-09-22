package message

import "ontology/wire"

// Marshal re-encodes the message. Fields are emitted in their exact
// input order, known and unknown interleaved as they were parsed, so
// the output of an unmodified message equals the parsed input byte
// for byte.
//
// Marshal does not modify m: calling it twice on the same Message
// yields identical bytes, and concurrent calls on the same Message
// are safe as long as no goroutine mutates it.
func (m *Message) Marshal() []byte {
	return m.appendTo(make([]byte, 0, m.encodedLen()))
}

// appendTo appends the encoding of m to buf.
func (m *Message) appendTo(buf []byte) []byte {
	for _, s := range m.slots {
		switch s.kind {
		case slotUnknown:
			buf = append(buf, m.unk.Field(s.unkIdx).Data...)
		case slotVarint:
			buf = wire.AppendHeader(buf, s.num, wire.Varint, 0)
			buf = wire.AppendVarint(buf, s.vint)
		case slotBytes:
			buf = wire.AppendHeader(buf, s.num, wire.Bytes, len(s.data))
			buf = append(buf, s.data...)
		case slotMessage:
			payload := s.child.Marshal()
			buf = wire.AppendHeader(buf, s.num, wire.Message, len(payload))
			buf = append(buf, payload...)
		}
	}
	return buf
}

// encodedLen estimates the output size to avoid reallocation.
func (m *Message) encodedLen() int {
	n := m.unk.TotalBytes()
	for _, s := range m.slots {
		switch s.kind {
		case slotVarint:
			n += wire.VarintLen(uint64(s.num)) + 1 + wire.VarintLen(s.vint)
		case slotBytes:
			n += wire.VarintLen(uint64(s.num)) + 1 + wire.VarintLen(uint64(len(s.data))) + len(s.data)
		case slotMessage:
			inner := s.child.encodedLen()
			n += wire.VarintLen(uint64(s.num)) + 1 + wire.VarintLen(uint64(inner)) + inner
		}
	}
	return n
}
