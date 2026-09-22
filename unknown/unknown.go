// Package unknown is the container for fields a parser does not
// recognize. It preserves unknown fields exactly as they appeared on the
// wire so they can be written back verbatim, keeping forward-compatible
// round-trips lossless.
//
// The package deliberately does not depend on the wire package: a Field
// stores its wire type as a raw byte and its complete encoded form as raw
// bytes, so write-back is byte-exact by construction (even for
// non-canonical varints) and needs no re-encoding logic.
package unknown

import "sort"

// Field is one unknown field.
//
// Raw holds the complete encoded field: [number varint][type byte]
// [length varint if any][payload]. It is a private copy, never an alias
// of the parser's input buffer. For Message-typed fields Raw contains the
// nested message verbatim, which is how nested unknown fields survive
// recursively.
type Field struct {
	Number uint64
	Type   byte
	Raw    []byte
}

// Set is an ordered collection of unknown fields. The zero value is ready
// to use. A Set belongs to one message and must not be shared across
// goroutines unless read-only.
type Set struct {
	fields []Field
}

// Add appends f to the set, copying f.Raw. Duplicate field numbers are
// kept as-is: never deduplicated, never merged, never reordered.
func (s *Set) Add(f Field) {
	raw := make([]byte, len(f.Raw))
	copy(raw, f.Raw)
	f.Raw = raw
	s.fields = append(s.fields, f)
}

// Len returns the number of unknown fields in the set.
func (s *Set) Len() int { return len(s.fields) }

// At returns the i-th field in original wire order. The returned Raw
// slice is shared with the set; treat it as read-only.
func (s *Set) At(i int) Field { return s.fields[i] }

// Fields returns a copy of all fields in original wire order.
func (s *Set) Fields() []Field {
	out := make([]Field, len(s.fields))
	copy(out, s.fields)
	return out
}

// Sorted returns a copy of the fields ordered by field number. The sort
// is stable, so fields with equal numbers keep their relative wire order.
// Write-back for round-tripping never uses this: it preserves the
// original interleaving. Sorted exists for callers that want a canonical
// inspection order.
func (s *Set) Sorted() []Field {
	out := s.Fields()
	sort.SliceStable(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out
}

// AppendTo writes every field back, in original wire order, appending the
// raw bytes to dst. It never modifies the set.
func (s *Set) AppendTo(dst []byte) []byte {
	for _, f := range s.fields {
		dst = append(dst, f.Raw...)
	}
	return dst
}

// AppendFieldTo writes back the single field at index i.
func (s *Set) AppendFieldTo(dst []byte, i int) []byte {
	return append(dst, s.fields[i].Raw...)
}
