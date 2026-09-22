// Package unknown is the container for fields a parser does not recognize.
// It depends on no other package in this module.
//
// Ordering rule (derived from the round-trip invariant): a message must
// re-encode to bytes identical to its input, and known/unknown fields may
// be interleaved arbitrarily. Therefore unknown fields can never be
// reordered, grouped, or sorted by field number on write; they must be
// re-emitted in their exact arrival order. Set preserves that order and
// keeps each field's complete raw encoding so re-emission is byte-exact,
// even for non-canonical varints in the original input.
package unknown

// Field is a single unrecognized field stored as its complete raw
// encoding (header and payload), exactly as read from the input.
type Field struct {
	Number uint64 // field number, decoded for introspection
	Raw    []byte // full encoded field, header included
}

// Set is an ordered container of unknown fields. The zero value is ready
// to use. A Set belongs to one message and is not safe for concurrent
// mutation, but distinct Sets may be used concurrently.
type Set struct {
	fields []Field
}

// Add appends a field, preserving arrival order. Repeated field numbers
// are kept as-is: never deduplicated, never merged.
func (s *Set) Add(f Field) {
	s.fields = append(s.fields, f)
}

// Len returns the number of stored fields.
func (s *Set) Len() int {
	return len(s.fields)
}

// At returns the i-th field in arrival order.
func (s *Set) At(i int) Field {
	return s.fields[i]
}

// Fields returns a copy of the stored fields in original wire order.
func (s *Set) Fields() []Field {
	out := make([]Field, len(s.fields))
	copy(out, s.fields)
	return out
}

// AppendTo re-emits all fields onto dst in their original arrival order.
func (s *Set) AppendTo(dst []byte) []byte {
	for _, f := range s.fields {
		dst = append(dst, f.Raw...)
	}
	return dst
}
