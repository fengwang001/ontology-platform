// Package ontology provides a stable, multi-key in-memory sorter for
// rows represented as map[string]any.
package ontology

// SortKey describes a single ordering key.
type SortKey struct {
	// Field is the row key to compare on.
	Field string
	// Desc reverses the value comparison only. It never affects null
	// placement and never reverses the relative order of equal rows.
	Desc bool
	// NullsFirst places null values (missing key, nil, or NaN) before
	// non-null values. Null placement is orthogonal to Desc.
	NullsFirst bool
}

// Sorter sorts rows by an ordered list of keys. A Sorter holds no
// mutable state and is safe for concurrent use by multiple goroutines;
// per-call statistics never leak between calls.
type Sorter struct {
	keys []SortKey
}

// New returns a Sorter using the given keys in priority order. The
// keys are copied, so later caller-side mutation cannot affect sorting.
func New(keys ...SortKey) *Sorter {
	copied := make([]SortKey, len(keys))
	copy(copied, keys)
	return &Sorter{keys: copied}
}
