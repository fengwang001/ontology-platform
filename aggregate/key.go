package aggregate

// MissingKind identifies how a single grouping-key column is absent on a row.
type MissingKind int

const (
	// Present means the property exists, is non-nil and is not the empty string.
	Present MissingKind = iota
	// EmptyString means the property exists and its value is the empty string "".
	EmptyString
	// Null means the property exists but its value is nil.
	Null
	// Absent means the property does not exist on the row at all.
	Absent
)

func (m MissingKind) String() string {
	switch m {
	case EmptyString:
		return "empty_string"
	case Null:
		return "null"
	case Absent:
		return "absent"
	default:
		return "present"
	}
}

// KeyPart describes one column of a composite group key.
// Exactly one of Value or Missing is meaningful: when Kind is Present the
// Value field holds the grouping value, otherwise Missing reports which of
// the three distinguishable missing situations the column fell into.
type KeyPart struct {
	Name    string
	Kind    MissingKind
	Missing MissingKind
	Value   any
}

// GroupKey is an ordered composite grouping key. Columns correspond 1:1 to
// the grouping attribute names passed to NewAggregator.
type GroupKey struct {
	Columns []KeyPart
}
