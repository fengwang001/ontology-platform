package message

// FieldKind enumerates the value kinds a schema can declare.
type FieldKind int

const (
	// KindVarint is a uint64 encoded as a varint.
	KindVarint FieldKind = iota
	// KindBytes is an opaque byte slice.
	KindBytes
	// KindMessage is a nested message parsed with another Schema.
	KindMessage
)

// Field declares one known field.
type Field struct {
	Number uint64
	Kind   FieldKind
	// Schema describes nested messages; only valid for KindMessage.
	Schema *Schema
	// Repeated fields keep every occurrence as separate values in
	// original order. Non-repeated fields keep the first value.
	Repeated bool
}

// Schema maps field numbers to declarations. A nil *Schema parses
// every field as unknown, which still round-trips exactly.
type Schema struct {
	fields map[uint64]Field
}

// NewSchema builds a schema from the given declarations.
func NewSchema(decls ...Field) *Schema {
	s := &Schema{fields: make(map[uint64]Field, len(decls))}
	for _, d := range decls {
		s.fields[d.Number] = d
	}
	return s
}

// lookup returns the declaration for num, if any.
func (s *Schema) lookup(num uint64) (Field, bool) {
	if s == nil {
		return Field{}, false
	}
	d, ok := s.fields[num]
	return d, ok
}

// wireType maps a declared kind to its wire-type byte.
func (d Field) wireType() byte {
	switch d.Kind {
	case KindBytes:
		return 1
	case KindMessage:
		return 2
	default:
		return 0
	}
}
