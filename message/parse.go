package message

import (
	"fmt"

	"ontology/unknown"
	"ontology/wire"
)

// Parse decodes data into a Message. On any error it returns nil and the
// error; no partially parsed structure is ever exposed. The returned
// Message owns all of its bytes and does not alias data.
func Parse(data []byte, lim Limits) (*Message, error) {
	if lim.MaxMessageBytes > 0 && len(data) > lim.MaxMessageBytes {
		return nil, fmt.Errorf("%w: %d bytes", ErrMessageTooLarge, len(data))
	}
	p := &parser{lim: lim}
	return p.parse(data, len(data), 0, 1)
}

// parser carries the limits and the running unknown-field count for one
// top-level parse.
type parser struct {
	lim      Limits
	unknowns int
}

// parse decodes fields from buf[0:limit]. buf may extend beyond limit
// (when parsing a nested message, buf is the rest of the outer buffer) so
// that a field crossing the limit boundary can be reported as a nested
// length mismatch rather than a length overflow. base is the absolute
// offset of buf[0] in the top-level input; depth starts at 1.
func (p *parser) parse(buf []byte, limit, base, depth int) (*Message, error) {
	if p.lim.MaxDepth > 0 && depth > p.lim.MaxDepth {
		return nil, fmt.Errorf("%w: depth %d at offset %d", ErrDepthExceeded, depth, base)
	}
	m := &Message{}
	off := 0
	for off < limit {
		f, err := wire.ParseHeader(buf[off:])
		if err != nil {
			if e, ok := err.(*wire.Error); ok {
				e.Offset += base + off
			}
			return nil, err
		}
		if off+f.Total() > limit {
			return nil, &wire.Error{
				Kind:   wire.KindNestedLengthMismatch,
				Offset: base + off,
			}
		}
		if p.lim.MaxPayloadBytes > 0 && f.Payload > p.lim.MaxPayloadBytes {
			return nil, fmt.Errorf("%w: field %d payload is %d bytes at offset %d",
				ErrPayloadTooLarge, f.Number, f.Payload, base+off)
		}
		if err := p.addField(m, f, buf[off:], base+off, depth); err != nil {
			return nil, err
		}
		off += f.Total()
	}
	return m, nil
}

// addField stores one parsed field into m. raw starts at the field's
// first byte and extends to the end of the current buffer.
func (p *parser) addField(m *Message, f wire.Field, raw []byte, absOff, depth int) error {
	total := f.Total()
	switch {
	case f.Number == fieldID && f.Type == wire.Varint:
		m.ids = append(m.ids, idOcc{raw: dup(raw[:total]), val: f.Value})
		m.order = append(m.order, slot{kind: slotID, idx: len(m.ids) - 1})
	case f.Number == fieldName && f.Type == wire.Bytes:
		m.names = append(m.names, nameOcc{
			raw: dup(raw[:total]),
			val: dup(raw[f.Header:total]),
		})
		m.order = append(m.order, slot{kind: slotName, idx: len(m.names) - 1})
	case f.Number == fieldChild && f.Type == wire.Message:
		child, err := p.parse(raw[f.Header:], f.Payload, absOff+f.Header, depth+1)
		if err != nil {
			return err
		}
		m.childs = append(m.childs, childOcc{raw: dup(raw[:total]), child: child})
		m.order = append(m.order, slot{kind: slotChild, idx: len(m.childs) - 1})
	default:
		p.unknowns++
		if p.lim.MaxUnknownFields > 0 && p.unknowns > p.lim.MaxUnknownFields {
			return fmt.Errorf("%w: field %d at offset %d", ErrTooManyUnknowns, f.Number, absOff)
		}
		m.unk.Add(unknown.Field{Number: f.Number, Raw: dup(raw[:total])})
		m.order = append(m.order, slot{kind: slotUnknown, idx: m.unk.Len() - 1})
	}
	return nil
}

func dup(b []byte) []byte {
	c := make([]byte, len(b))
	copy(c, b)
	return c
}
