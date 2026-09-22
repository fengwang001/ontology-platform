// Package unknown is the container for fields the local schema does
// not recognise. It preserves each unknown field's original bytes
// and their relative order so they can be written back verbatim.
// It depends on nothing outside the standard library.
package unknown

// Field is a single unrecognised field.
//
// Raw holds the field's complete original encoding — field-number
// varint, wire-type byte, optional length prefix and payload —
// exactly as read from the input. Keeping the raw bytes (instead of
// a decoded value) is what guarantees byte-for-byte round-trip even
// for encodings this version of the parser cannot interpret.
type Field struct {
	Number uint64
	Raw    []byte
}

// Set is an ordered container of unknown fields.
//
// Ordering rule: fields are kept in exact insertion (encounter)
// order. Entries are never deduplicated, merged or reordered, so
// repeated occurrences of the same field number keep their identity
// and their sequence.
type Set struct {
	fields []Field
}

// Add appends f to the set, preserving encounter order.
func (s *Set) Add(f Field) { s.fields = append(s.fields, f) }

// Len returns the number of stored fields.
func (s *Set) Len() int { return len(s.fields) }

// At returns the i-th stored field.
func (s *Set) At(i int) Field { return s.fields[i] }

// Fields returns a copy of the stored fields, in order.
func (s *Set) Fields() []Field {
	out := make([]Field, len(s.fields))
	copy(out, s.fields)
	return out
}

// AppendTo writes every stored field back, in original order.
func (s *Set) AppendTo(dst []byte) []byte {
	for _, f := range s.fields {
		dst = append(dst, f.Raw...)
	}
	return dst
}
