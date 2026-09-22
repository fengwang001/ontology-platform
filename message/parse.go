package message

import (
	"ontology/unknown"
	"ontology/wire"
)

// knownType maps known field numbers to their expected wire type.
func knownType(num uint64) (wire.Type, bool) {
	switch num {
	case fieldID:
		return wire.Varint, true
	case fieldName:
		return wire.Bytes, true
	case fieldChild:
		return wire.Message, true
	}
	return 0, false
}

// Parse decodes buf into a Msg.
//
// On any failure it returns the zero Msg and a non-nil error: no
// partially decoded state is ever exposed to the caller. Errors are
// either *wire.Error (syntax, distinguishable via wire.IsKind) or
// *LimitError (resource ceilings).
//
// The returned message owns all of its data; it shares no memory
// with buf, which may be reused or mutated after Parse returns.
func Parse(buf []byte, lim Limits) (Msg, error) {
	if lim.MaxBytes > 0 && len(buf) > lim.MaxBytes {
		return Msg{}, &LimitError{Resource: "message-bytes", Limit: lim.MaxBytes, Offset: 0}
	}
	return parseMsg(buf, 0, len(buf), 1, lim)
}

// parseMsg parses fields from buf[off:end]. Offsets in errors are
// absolute positions in the top-level buffer.
func parseMsg(buf []byte, off, end, depth int, lim Limits) (Msg, error) {
	maxDepth := lim.MaxDepth
	if maxDepth <= 0 {
		maxDepth = defaultMaxDepth
	}
	if depth > maxDepth {
		return Msg{}, &LimitError{Resource: "depth", Limit: maxDepth, Offset: off}
	}
	var m Msg
	for off < end {
		fieldStart := off
		h, err := wire.ReadHeader(buf, off)
		if err != nil {
			return Msg{}, err
		}
		off = h.Next
		kt, known := knownType(h.Number)
		known = known && kt == h.Type
		e := entry{known: known, num: h.Number, typ: h.Type}
		switch h.Type {
		case wire.Varint:
			v, n, err := wire.ReadVarint(buf, off)
			if err != nil {
				return Msg{}, err
			}
			off = n
			if known {
				e.u64 = v
			}
		case wire.Bytes, wire.Message:
			l, n, err := wire.ReadLength(buf, off)
			if err != nil {
				return Msg{}, err
			}
			if lim.MaxFieldPayload > 0 && l > lim.MaxFieldPayload {
				return Msg{}, &LimitError{Resource: "field-payload", Limit: lim.MaxFieldPayload, Offset: off}
			}
			if known && h.Type == wire.Message {
				child, err := parseMsg(buf, n, n+l, depth+1, lim)
				if err != nil {
					return Msg{}, err
				}
				e.child = &child
			} else if known {
				e.data = append([]byte(nil), buf[n:n+l]...)
			}
			off = n + l
		}
		if off > end {
			// The field crosses the boundary of its enclosing
			// nested message: declared length and actual content
			// disagree.
			return Msg{}, &wire.Error{Kind: wire.KindNestedLengthMismatch, Offset: fieldStart}
		}
		if !known {
			if lim.MaxUnknown > 0 && m.unk.Len() >= lim.MaxUnknown {
				return Msg{}, &LimitError{Resource: "unknown-fields", Limit: lim.MaxUnknown, Offset: fieldStart}
			}
			raw := append([]byte(nil), buf[fieldStart:off]...)
			m.unk.Add(unknown.Field{Number: h.Number, Raw: raw})
			e.unkIndex = m.unk.Len() - 1
		}
		m.entries = append(m.entries, e)
	}
	return m, nil
}
