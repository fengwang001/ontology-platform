// Package unknown is the container for fields a parser does not
// recognize: it preserves them verbatim, keeps their order, and
// re-emits them on demand. It depends on no other package.
//
// Ordering rule, derived from the byte-equivalence invariant:
//
//	The input may interleave known and unknown fields arbitrarily and
//	re-encoding a parsed message must reproduce the input byte for
//	byte. The only emission order compatible with both facts is the
//	exact input order. Therefore Set is a strictly order-preserving
//	sequence: fields are kept in arrival order, and are never sorted
//	by field number, never deduplicated and never merged. Repeated
//	occurrences of the same field number stay distinct and keep their
//	relative order. The message package is responsible for
//	interleaving these fields with known fields at their original
//	positions; this package guarantees the unknown subsequence itself
//	is stable.
package unknown

// Field is one unrecognized field, preserved verbatim.
type Field struct {
	Num  uint32 // field number as decoded from the header
	Type byte   // wire-type byte as found in the input
	Data []byte // complete encoded field: header + payload
}

// Set is an order-preserving sequence of unknown fields.
//
// The zero value is ready to use. A Set is not safe for concurrent
// mutation; independent Sets (e.g. one per parsed message) can be used
// from different goroutines without synchronization.
type Set struct {
	fields []Field
}

// Add appends a field and returns its index. data must be the complete
// encoded field (header and payload); it is copied, so the caller may
// reuse or mutate its buffer afterwards.
func (s *Set) Add(num uint32, typ byte, data []byte) int {
	cp := make([]byte, len(data))
	copy(cp, data)
	s.fields = append(s.fields, Field{Num: num, Type: typ, Data: cp})
	return len(s.fields) - 1
}

// Len returns the number of preserved fields.
func (s *Set) Len() int { return len(s.fields) }

// Field returns the i-th preserved field in arrival order. The
// returned Data slice is shared with the Set and must be treated as
// read-only.
func (s *Set) Field(i int) Field { return s.fields[i] }

// AppendTo appends the verbatim bytes of every field, in arrival
// order, to dst and returns the extended slice. It does not modify
// the Set, so repeated calls produce identical output.
func (s *Set) AppendTo(dst []byte) []byte {
	for _, f := range s.fields {
		dst = append(dst, f.Data...)
	}
	return dst
}

// TotalBytes returns the summed encoded size of all preserved fields.
func (s *Set) TotalBytes() int {
	n := 0
	for _, f := range s.fields {
		n += len(f.Data)
	}
	return n
}
