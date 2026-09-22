package message

import (
	"ontology/unknown"
	"ontology/wire"
)

// Parse decodes buf using schema. On any failure it returns (nil, err):
// no partially built message ever escapes. The error is either a
// *wire.ParseError (offset measured from buf) or wraps one of
// ErrTypeMismatch / ErrTooDeep / wire.ErrSizeLimit /
// unknown.ErrTooManyUnknown. The parsed payloads never alias buf.
func Parse(buf []byte, schema *Schema, lim Limits) (*Msg, error) {
	max := lim.messageBytes()
	if max > 0 && len(buf) > max {
		return nil, wire.NewParseError(wire.ErrSizeLimit, max)
	}
	m := New(schema)
	if _, err := m.parseFields(buf, 0, len(buf), lim, 1); err != nil {
		return nil, err
	}
	return m, nil
}

// parseFields fills m from buf[start:end] and returns the first
// unconsumed index. depth is 1 for the top level. Callers parsing a
// nested payload treat consumed != end as ErrNestedLength. It runs on
// a freshly built message only, so an error discards all state with
// the caller discarding the message.
func (m *Msg) parseFields(buf []byte, start, end int, lim Limits, depth int) (int, error) {
	pos := start
	for pos < end {
		h, next, err := wire.ConsumeHeader(buf, pos, lim.fieldPayloadBytes())
		if err != nil {
			return pos, err
		}
		decl, isKnown := m.schema.lookup(h.Number)
		switch {
		case isKnown && decl.wireType() != h.Type:
			return pos, wire.NewParseError(ErrTypeMismatch, pos)
		case isKnown:
			if err := m.parseKnown(buf, pos, h, decl, lim, depth); err != nil {
				return pos, err
			}
		default:
			if err := m.unk.Add(unknown.Field{
				Number:  h.Number,
				Type:    h.Type,
				Payload: buf[h.PayloadStart:h.PayloadEnd],
			}, lim.unknown()); err != nil {
				return pos, wire.NewParseError(err, pos)
			}
			m.slots = append(m.slots, slot{uIdx: m.unk.Len() - 1})
		}
		pos = next
	}
	return pos, nil
}

// parseKnown parses one header known to the schema and appends a slot.
func (m *Msg) parseKnown(buf []byte, fieldPos int, h wire.Header, decl Field, lim Limits, depth int) error {
	e := &knownEntry{num: h.Number, wt: h.Type}
	raw := buf[h.PayloadStart:h.PayloadEnd]
	e.raw = make([]byte, len(raw))
	copy(e.raw, raw)

	switch decl.Kind {
	case KindVarint:
		v, _, err := wire.ConsumeVarint(buf, h.PayloadStart)
		if err != nil {
			return err
		}
		e.u = v
	case KindBytes:
		e.b = cloneBytes(e.raw)
	case KindMessage:
		if depth >= lim.nesting() {
			return wire.NewParseError(ErrTooDeep, fieldPos)
		}
		nested := New(decl.Schema)
		consumed, err := nested.parseFields(buf, h.PayloadStart, h.PayloadEnd, lim, depth+1)
		if err != nil {
			return err
		}
		if consumed != h.PayloadEnd {
			return wire.NewParseError(wire.ErrNestedLength, fieldPos)
		}
		e.m = nested
	}
	m.slots = append(m.slots, slot{known: true, entry: e})
	return nil
}

func cloneBytes(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
