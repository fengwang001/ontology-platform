package unknown

// Fields is an ordered container of unknown fields.
//
// Ordering rule (derived from the byte-for-byte round-trip
// invariant): fields must be written back in the exact relative order
// in which they appeared in the input. The input interleaves known
// and unknown fields; the message package stores a single merged
// sequence and unknown.Fields preserves its own subsequence within
// it. Repeated occurrences of one field number are never deduplicated
// or merged, so Add always appends and never reorders.
type Fields struct {
	items []Field
}

// Len returns the number of retained fields.
func (f *Fields) Len() int { return len(f.items) }

// At returns the field at index i.
func (f *Fields) At(i int) Field { return f.items[i] }

// All returns the retained fields in insertion order. The returned
// slice is a copy and may be modified by the caller.
func (f *Fields) All() []Field {
	out := make([]Field, len(f.items))
	copy(out, f.items)
	return out
}

// Add appends a field, copying payload so the stored value never
// aliases the parser's input buffer. A negative max means unlimited.
// If appending would exceed max fields the container is left
// untouched and ErrTooManyUnknown is returned.
func (f *Fields) Add(field Field, max int) error {
	if max >= 0 && len(f.items) >= max {
		return ErrTooManyUnknown
	}
	cp := Field{Number: field.Number, Type: field.Type}
	if field.Payload != nil {
		cp.Payload = make([]byte, len(field.Payload))
		copy(cp.Payload, field.Payload)
	}
	f.items = append(f.items, cp)
	return nil
}

// Reset removes all retained fields.
func (f *Fields) Reset() { f.items = nil }

// AppendWrite appends the wire encoding of every retained field, in
// insertion order, to dst.
func (f *Fields) AppendWrite(dst []byte) []byte {
	for i := range f.items {
		dst = f.items[i].Append(dst)
	}
	return dst
}

// Append appends the wire encoding of the field to dst.
//
// It encodes one field: number varint, type byte, optional
// length varint, then the verbatim payload.
func (fld Field) Append(dst []byte) []byte {
	dst = appendVarint(dst, fld.Number)
	dst = append(dst, fld.Type)
	if lengthPrefixed(fld.Type) {
		dst = appendVarint(dst, uint64(len(fld.Payload)))
	}
	return append(dst, fld.Payload...)
}
