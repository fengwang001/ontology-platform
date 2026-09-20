package ontology

import "math"

// NullKind explains why a row's field is treated as null when sorting.
// Missing keys and nil values sort identically, but NullKindOf lets
// callers tell them apart independently of any sort.
type NullKind int

const (
	// NotNull means the field holds a regular value ("" included).
	NotNull NullKind = iota
	// Missing means the key is absent from the row map.
	Missing
	// NilValue means the key is present with a nil value.
	NilValue
	// NaNValue means the value is a float64 NaN, which sorts as null.
	NaNValue
)

// String implements fmt.Stringer.
func (k NullKind) String() string {
	switch k {
	case NotNull:
		return "not-null"
	case Missing:
		return "missing"
	case NilValue:
		return "nil"
	case NaNValue:
		return "nan"
	}
	return "unknown"
}

// NullKindOf reports whether row[field] is null for sorting, and why.
// An empty string is a normal value and reports NotNull.
func NullKindOf(row map[string]any, field string) NullKind {
	v, ok := row[field]
	if !ok {
		return Missing
	}
	if v == nil {
		return NilValue
	}
	if f, isFloat := v.(float64); isFloat && math.IsNaN(f) {
		return NaNValue
	}
	return NotNull
}
