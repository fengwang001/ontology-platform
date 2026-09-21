package ontology

import "fmt"

// KeyKind classifies how a group's key was derived from a row.
type KeyKind int

const (
	// KeyMissing means the group-key column was absent from the row.
	KeyMissing KeyKind = iota
	// KeyNull means the group-key column was present with a nil value.
	KeyNull
	// KeyValue means the group-key column held a concrete value.
	KeyValue
)

// String returns a stable human-readable name for the kind.
func (k KeyKind) String() string {
	switch k {
	case KeyMissing:
		return "missing"
	case KeyNull:
		return "null"
	default:
		return "value"
	}
}

// GroupKey identifies one group. Missing, Null and Value("") are three
// distinct groups. Groups are ordered by Kind, then by Value ascending.
type GroupKey struct {
	Kind  KeyKind
	Value string
}

// String renders the key in a distinguishable, stable form.
func (g GroupKey) String() string {
	if g.Kind == KeyValue {
		return fmt.Sprintf("value(%q)", g.Value)
	}
	return g.Kind.String()
}

// encoded returns the internal map key; the three null-ish kinds can never
// collide with each other or with any value.
func (g GroupKey) encoded() string {
	switch g.Kind {
	case KeyMissing:
		return "\x00m"
	case KeyNull:
		return "\x00n"
	default:
		return "\x00v" + g.Value
	}
}

// groupKeyOf derives the GroupKey for a row. Non-string values are compared
// by their fmt "%v" form.
func groupKeyOf(row map[string]any, column string) GroupKey {
	v, ok := row[column]
	if !ok {
		return GroupKey{Kind: KeyMissing}
	}
	if v == nil {
		return GroupKey{Kind: KeyNull}
	}
	if s, ok := v.(string); ok {
		return GroupKey{Kind: KeyValue, Value: s}
	}
	return GroupKey{Kind: KeyValue, Value: fmt.Sprintf("%v", v)}
}

// groupLess orders groups: by Kind, then by Value ascending.
func groupLess(a, b GroupKey) bool {
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	return a.Value < b.Value
}
