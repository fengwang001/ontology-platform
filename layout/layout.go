// Package layout defines fixed-width, fixed-offset record schemas.
//
// Fields are laid out tightly, in declaration order, with no padding: the
// offset of field i is the sum of the widths of all preceding fields.
package layout

import "errors"

// Sentinel errors for schema construction.
var (
	ErrEmptyFieldName = errors.New("layout: empty field name")
	ErrInvalidWidth   = errors.New("layout: field width must be 1, 2, 4 or 8")
	ErrDuplicateField = errors.New("layout: duplicate field name")
)

// Field describes one fixed-width slot of a record.
type Field struct {
	Name   string
	Offset int
	Width  int
	Signed bool
}

// Schema is an ordered, tightly packed set of fields with hash lookup by name.
type Schema struct {
	fields []Field
	index  map[string]int
	size   int
	// probeCount records how many field names were compared one by one
	// during the most recent FieldByName call. It is unexported on purpose.
	probeCount int
}

// NewSchema derives offsets from the given specs (their Offset is ignored and
// recomputed as the running sum of widths). No padding is inserted.
func NewSchema(specs []Field) (*Schema, error) {
	index := make(map[string]int, len(specs))
	fields := make([]Field, 0, len(specs))
	off := 0
	for _, f := range specs {
		if f.Name == "" {
			return nil, ErrEmptyFieldName
		}
		switch f.Width {
		case 1, 2, 4, 8:
		default:
			return nil, ErrInvalidWidth
		}
		if _, dup := index[f.Name]; dup {
			return nil, ErrDuplicateField
		}
		index[f.Name] = len(fields)
		fields = append(fields, Field{Name: f.Name, Offset: off, Width: f.Width, Signed: f.Signed})
		off += f.Width
	}
	return &Schema{fields: fields, index: index, size: off}, nil
}

// FieldByName locates a field by hashed lookup. It records how many field
// names had to be compared: exactly one on a direct hash hit, regardless of
// the total number of fields.
func (s *Schema) FieldByName(name string) (Field, bool) {
	s.probeCount = 0
	i, ok := s.index[name]
	if !ok {
		return Field{}, false
	}
	s.probeCount = 1 // hash-directed slot: a single key-equality comparison
	return s.fields[i], true
}

// Has reports name membership without touching the probe counter.
func (s *Schema) Has(name string) bool {
	_, ok := s.index[name]
	return ok
}

// Fields returns the ordered field list as a defensive copy.
func (s *Schema) Fields() []Field {
	out := make([]Field, len(s.fields))
	copy(out, s.fields)
	return out
}

// Size returns the total record width, i.e. the sum of all field widths.
func (s *Schema) Size() int { return s.size }
