package message

import (
	"errors"
	"ontology/unknown"
	"ontology/wire"
)

// Parse decodes one top-level message from data. On any failure it
// returns a nil Message and a *wire.Error (errors.Is matches the
// sentinels in the wire package and in this package); no partially
// parsed state ever reaches the caller. The returned Message owns copies
// of everything it retains: mutating data afterwards has no effect.
func (p *Parser) Parse(data []byte) (*Message, error) {
	if len(data) > p.opts.MaxMessageBytes {
		return nil, &wire.Error{Offset: 0, Err: ErrMessageTooLarge}
	}
	st := &parseState{p: p}
	return st.parse(data, 0, 0)
}

type parseState struct {
	p        *Parser
	unknowns int
}

// parse decodes the message in buf, whose bytes start at absolute offset
// base in the top-level input. depth is the current nesting level (0 for
// the top-level message).
func (st *parseState) parse(buf []byte, base, depth int) (*Message, error) {
	if depth > st.p.opts.MaxDepth {
		return nil, &wire.Error{Offset: base, Err: ErrMaxDepth}
	}
	m := &Message{
		parser:  st.p,
		values:  map[int]any{},
		dirty:   map[int]bool{},
		deleted: map[int]bool{},
	}
	off := 0
	for off < len(buf) {
		f, n, err := wire.ReadField(buf[off:])
		if err != nil {
			return nil, st.rebase(err, base+off, depth)
		}
		if len(f.Payload) > st.p.opts.MaxFieldBytes {
			return nil, &wire.Error{Offset: base + off, Err: ErrFieldTooLarge}
		}
		raw := make([]byte, n)
		copy(raw, buf[off:off+n])
		if err := st.addField(m, f, raw, base+off, depth); err != nil {
			return nil, err
		}
		off += n
	}
	return m, nil
}

// rebase shifts a wire error to absolute offsets. Inside a nested region
// (depth > 0) running out of bytes means the declared nested length did
// not match the content, so truncation becomes ErrLengthMismatch.
func (st *parseState) rebase(err error, base, depth int) error {
	var we *wire.Error
	if !errors.As(err, &we) {
		return err
	}
	e := we.Err
	if depth > 0 && errors.Is(e, wire.ErrTruncated) {
		e = wire.ErrLengthMismatch
	}
	return &wire.Error{Offset: base + we.Offset, Err: e}
}

func (st *parseState) addField(m *Message, f wire.Field, raw []byte, base, depth int) error {
	num := int(f.Number)
	t, known := st.p.schema[num]
	if known && t == f.Type {
		return st.addKnown(m, f, raw, base, depth)
	}
	st.unknowns++
	if st.unknowns > st.p.opts.MaxUnknownFields {
		return &wire.Error{Offset: base, Err: ErrTooManyUnknownFields}
	}
	if f.Type == wire.Message {
		// Validate the nested payload recursively (depth limit and
		// syntax apply inside unknown fields too), but keep the raw
		// bytes: nested unknown fields survive verbatim.
		if _, err := st.parse(f.Payload, base+f.HeaderLen, depth+1); err != nil {
			return err
		}
	}
	m.unknowns.Add(unknown.Field{Number: f.Number, Type: byte(f.Type), Raw: raw})
	m.entries = append(m.entries, entry{known: false, num: num, uref: m.unknowns.Len() - 1})
	return nil
}

func (st *parseState) addKnown(m *Message, f wire.Field, raw []byte, base, depth int) error {
	num := int(f.Number)
	switch f.Type {
	case wire.Varint:
		m.values[num] = f.Varint
	case wire.Bytes:
		cp := make([]byte, len(f.Payload))
		copy(cp, f.Payload)
		m.values[num] = cp
	case wire.Message:
		sub, err := st.parse(f.Payload, base+f.HeaderLen, depth+1)
		if err != nil {
			return err
		}
		m.values[num] = sub
	}
	m.entries = append(m.entries, entry{known: true, num: num, raw: raw})
	return nil
}
