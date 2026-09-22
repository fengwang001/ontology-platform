package message

import "ontology/wire"

// dup copies a byte slice so parsed structures never alias the
// caller's input buffer.
func dup(b []byte) []byte {
	c := make([]byte, len(b))
	copy(c, b)
	return c
}

// abs shifts a wire error's offset into the absolute coordinate of
// the top-level buffer.
func abs(err error, base int) error {
	if we, ok := err.(*wire.Error); ok {
		we.Offset += base
	}
	return err
}

// nested converts a length overflow inside a nested message into the
// distinct nested-length-mismatch kind: the parent's declared length
// does not line up with the fields actually inside it.
func nested(err error, depth int) error {
	if depth > 1 {
		if we, ok := err.(*wire.Error); ok && we.Kind == wire.KindLength {
			return &wire.Error{Kind: wire.KindNestedLengthMismatch, Offset: we.Offset}
		}
	}
	return err
}

// readPayloadAt reads a length-prefixed payload at buf[off]. It
// returns the payload start offset and the offset just past the
// payload. The field-size limit is enforced before any copy happens.
func (p *parser) readPayloadAt(buf []byte, off, base, depth int) (int, int, error) {
	n, start, err := wire.ReadLength(buf, off)
	if err != nil {
		return 0, 0, nested(abs(err, base), depth)
	}
	if p.opts.MaxFieldBytes > 0 && n > p.opts.MaxFieldBytes {
		return 0, 0, &LimitError{Err: ErrFieldTooLarge, Offset: base + off}
	}
	return start, start + n, nil
}

// readPayload is readPayloadAt returning the payload sub-slice.
func (p *parser) readPayload(buf []byte, off, base, depth int) ([]byte, int, error) {
	start, end, err := p.readPayloadAt(buf, off, base, depth)
	if err != nil {
		return nil, 0, err
	}
	return buf[start:end], end, nil
}

// parseUnknown preserves one unrecognized field verbatim. The count
// limit is checked right after the header, before the payload is
// touched, so a rejected message leaves no partial state behind.
func (p *parser) parseUnknown(e *Envelope, buf []byte, off, next, base, depth int, h wire.Header) (int, error) {
	if p.opts.MaxUnknownFields > 0 && p.unknowns >= p.opts.MaxUnknownFields {
		return 0, &LimitError{Err: ErrTooManyUnknown, Offset: base + off}
	}
	var end int
	switch h.Type {
	case wire.Varint:
		_, end2, err := wire.ReadVarint(buf, next)
		if err != nil {
			return 0, abs(err, base)
		}
		end = end2
	default: // wire.Bytes or wire.Message
		start, end2, err := p.readPayloadAt(buf, next, base, depth)
		if err != nil {
			return 0, err
		}
		if h.Type == wire.Message {
			if err := p.validate(buf[start:end2], base+start, depth+1); err != nil {
				return 0, err
			}
		}
		end = end2
	}
	p.unknowns++
	e.Unknown.Add(h.Field, byte(h.Type), buf[off:end])
	e.order = append(e.order, ref{field: h.Field, known: false})
	return end, nil
}

// validate walks a nested message inside an unknown field without
// building a structure, so malformed or excessively deep nesting is
// rejected deterministically instead of overflowing the stack.
func (p *parser) validate(buf []byte, base, depth int) error {
	if p.opts.MaxDepth > 0 && depth > p.opts.MaxDepth {
		return &LimitError{Err: ErrDepthExceeded, Offset: base}
	}
	off := 0
	for off < len(buf) {
		h, next, err := wire.ReadHeader(buf, off)
		if err != nil {
			return abs(err, base)
		}
		var end int
		if h.Type == wire.Varint {
			_, end, err = wire.ReadVarint(buf, next)
			if err != nil {
				return abs(err, base)
			}
		} else {
			var start int
			start, end, err = p.readPayloadAt(buf, next, base, depth)
			if err != nil {
				return err
			}
			if h.Type == wire.Message {
				if err := p.validate(buf[start:end], base+start, depth+1); err != nil {
					return err
				}
			}
		}
		off = end
	}
	return nil
}
