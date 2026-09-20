package ontology

// FieldType is the declared type of a config field.
type FieldType int

const (
	// StringField holds string values.
	StringField FieldType = iota
	// IntField holds integer values, stored normalized as int64.
	IntField
)

// FieldDecl declares one named config field. Min and Max are inclusive
// bounds and only apply to IntField declarations.
type FieldDecl struct {
	Name string
	Type FieldType
	Min  int64
	Max  int64
}

// normalizeInt converts accepted integer inputs to int64.
func normalizeInt(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	default:
		return 0, false
	}
}

// validate checks one value against the declaration and returns the
// normalized value to store (integers are normalized to int64).
func (d FieldDecl) validate(v any) (any, error) {
	switch d.Type {
	case StringField:
		s, ok := v.(string)
		if !ok {
			return nil, &ValidationError{Field: d.Name, Kind: KindTypeMismatch, Value: v}
		}
		return s, nil
	case IntField:
		n, ok := normalizeInt(v)
		if !ok {
			return nil, &ValidationError{Field: d.Name, Kind: KindTypeMismatch, Value: v}
		}
		if n < d.Min || n > d.Max {
			return nil, &ValidationError{Field: d.Name, Kind: KindOutOfRange, Value: v}
		}
		return n, nil
	default:
		return nil, &ValidationError{Field: d.Name, Kind: KindTypeMismatch, Value: v}
	}
}
