package message

import (
	"errors"

	"ontology/wire"
)

// Parse decodes one message from data.
//
// On any failure it returns a nil *Message: no partially parsed
// structure ever leaks to the caller. On success the returned Message
// owns all of its data; mutating data afterwards has no effect on it.
// A nil opts uses DefaultOptions.
func Parse(data []byte, opts *Options) (*Message, error) {
	o := DefaultOptions()
	if opts != nil {
		o = opts.withDefaults()
	}
	if len(data) > o.MaxMessageBytes {
		return nil, &wire.ParseError{Err: ErrMessageTooLarge, Offset: 0}
	}
	st := &parseState{opts: o}
	return st.parseMessage(data, 0, 0)
}

// parseState carries limits and running counters through the recursion.
type parseState struct {
	opts     Options
	unknowns int
}

// parseMessage parses the field sequence that exactly fills buf. base
// is buf's offset within the original input (for error offsets) and
// depth is the nesting level (0 for the top-level message).
func (st *parseState) parseMessage(buf []byte, base, depth int) (*Message, error) {
	if depth > st.opts.MaxDepth {
		return nil, &wire.ParseError{Err: ErrDepthExceeded, Offset: base}
	}
	m := &Message{}
	off := 0
	for off < len(buf) {
		next, err := st.parseField(m, buf, off, base, depth)
		if err != nil {
			return nil, err
		}
		off = next
	}
	return m, nil
}

// parseField parses the single field starting at buf[off] and appends
// it to m. It returns the offset just past the field.
func (st *parseState) parseField(m *Message, buf []byte, off, base, depth int) (int, error) {
	h, err := wire.DecodeHeader(buf[off:])
	if err != nil {
		return 0, wire.AddOffset(st.convertNested(err, depth), base+off)
	}
	payloadAt := off + h.Size
	if h.Type == wire.Varint {
		return st.varintField(m, buf, off, payloadAt, base, depth, h)
	}
	if h.Len > st.opts.MaxPayloadBytes {
		return 0, &wire.ParseError{Err: ErrPayloadTooLarge, Offset: base + payloadAt}
	}
	payload := buf[payloadAt : payloadAt+h.Len]
	end := payloadAt + h.Len
	if h.Type == wire.Message && h.Num == fieldChild {
		child, err := st.parseMessage(payload, base+payloadAt, depth+1)
		if err != nil {
			return 0, err
		}
		m.slots = append(m.slots, slot{num: h.Num, kind: slotMessage, child: child})
		return end, nil
	}
	if h.Type == wire.Bytes && h.Num == fieldName {
		data := make([]byte, h.Len)
		copy(data, payload)
		m.slots = append(m.slots, slot{num: h.Num, kind: slotBytes, data: data})
		return end, nil
	}
	return st.unknownField(m, buf[off:end], h, base, off)
}

// varintField handles a field whose payload is a bare varint.
func (st *parseState) varintField(m *Message, buf []byte, off, payloadAt, base, depth int, h wire.Header) (int, error) {
	v, n, err := wire.DecodeVarint(buf[payloadAt:])
	if err != nil {
		return 0, wire.AddOffset(st.convertNested(err, depth), base+payloadAt)
	}
	if h.Num == fieldID {
		m.slots = append(m.slots, slot{num: h.Num, kind: slotVarint, vint: v})
		return payloadAt + n, nil
	}
	return st.unknownField(m, buf[off:payloadAt+n], h, base, off)
}

// unknownField preserves one field verbatim in the unknown.Set.
func (st *parseState) unknownField(m *Message, raw []byte, h wire.Header, base, off int) (int, error) {
	if st.unknowns >= st.opts.MaxUnknownFields {
		return 0, &wire.ParseError{Err: ErrTooManyUnknownFields, Offset: base + off}
	}
	idx := m.unk.Add(h.Num, byte(h.Type), raw)
	st.unknowns++
	m.slots = append(m.slots, slot{num: h.Num, kind: slotUnknown, unkIdx: idx})
	return off + len(raw), nil
}

// convertNested reclassifies a boundary error inside a nested message:
// when an inner field would cross the length declared for the nested
// message, the declared length and the actual content disagree.
func (st *parseState) convertNested(err error, depth int) error {
	if depth == 0 {
		return err
	}
	var pe *wire.ParseError
	if errors.As(err, &pe) && errors.Is(err, wire.ErrLengthOverflow) {
		return &wire.ParseError{Err: wire.ErrNestedLengthMismatch, Offset: pe.Offset}
	}
	return err
}
