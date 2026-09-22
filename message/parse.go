package message

import "ontology/wire"

// Parse decodes data into an Envelope. On any error it returns a nil
// message: no partially parsed structure ever leaks to the caller.
func Parse(data []byte, opts Options) (*Envelope, error) {
	if opts.MaxMessageBytes > 0 && len(data) > opts.MaxMessageBytes {
		return nil, &LimitError{Err: ErrMessageTooLarge, Offset: 0}
	}
	p := &parser{opts: opts}
	e, err := p.parseEnvelope(data, 0, 1)
	if err != nil {
		return nil, err
	}
	return e, nil
}

// Unmarshal parses data with DefaultOptions.
func Unmarshal(data []byte) (*Envelope, error) {
	return Parse(data, DefaultOptions())
}

type parser struct {
	opts     Options
	unknowns int
}

func (p *parser) parseEnvelope(buf []byte, base, depth int) (*Envelope, error) {
	if p.opts.MaxDepth > 0 && depth > p.opts.MaxDepth {
		return nil, &LimitError{Err: ErrDepthExceeded, Offset: base}
	}
	e := &Envelope{}
	off := 0
	for off < len(buf) {
		next, err := p.parseField(e, buf, off, base, depth)
		if err != nil {
			return nil, err
		}
		off = next
	}
	return e, nil
}

// parseField parses the field starting at buf[off] (absolute offset
// base+off) into e and returns the offset just past it.
func (p *parser) parseField(e *Envelope, buf []byte, off, base, depth int) (int, error) {
	h, next, err := wire.ReadHeader(buf, off)
	if err != nil {
		return 0, abs(err, base)
	}
	switch {
	case h.Field == fieldID && h.Type == wire.Varint:
		v, end, err := wire.ReadVarint(buf, next)
		if err != nil {
			return 0, abs(err, base)
		}
		e.ID, e.hasID, e.origID = v, true, v
		e.idOcc = append(e.idOcc, occurrence{raw: dup(buf[off:end])})
		e.order = append(e.order, ref{field: fieldID, known: true})
		return end, nil
	case h.Field == fieldName && h.Type == wire.Bytes:
		payload, end, err := p.readPayload(buf, next, base, depth)
		if err != nil {
			return 0, err
		}
		e.Name, e.hasName = dup(payload), true
		e.origName = dup(payload)
		e.nameOcc = append(e.nameOcc, occurrence{raw: dup(buf[off:end])})
		e.order = append(e.order, ref{field: fieldName, known: true})
		return end, nil
	case h.Field == fieldTags && h.Type == wire.Bytes:
		payload, end, err := p.readPayload(buf, next, base, depth)
		if err != nil {
			return 0, err
		}
		e.Tags = append(e.Tags, dup(payload))
		e.origTags = append(e.origTags, dup(payload))
		e.tagOcc = append(e.tagOcc, occurrence{raw: dup(buf[off:end])})
		e.order = append(e.order, ref{field: fieldTags, known: true})
		return end, nil
	case h.Field == fieldChild && h.Type == wire.Message:
		start, end, err := p.readPayloadAt(buf, next, base, depth)
		if err != nil {
			return 0, err
		}
		child, err := p.parseEnvelope(buf[start:end], base+start, depth+1)
		if err != nil {
			return 0, err
		}
		e.Child = child
		e.childOcc = append(e.childOcc, childOccurrence{
			raw:     dup(buf[off:end]),
			payload: dup(buf[start:end]),
		})
		e.order = append(e.order, ref{field: fieldChild, known: true})
		return end, nil
	default:
		return p.parseUnknown(e, buf, off, next, base, depth, h)
	}
}
