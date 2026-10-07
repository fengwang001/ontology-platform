package derived

// Value is an index key value. Index keys are scalar strings.
type Value = string

// Object is an object instance.
type Object struct {
	ID         string
	Type       string
	Properties map[string]Value
}

// Link is a directed instance link: From (downstream) --Type--> To (source).
type Link struct {
	From string
	To   string
	Type string
}

// Declaration binds an index name to a link type and a source property.
//
// The derived value of the index for a downstream instance is read from the
// instance reached through LinkType. SourceProperty is either a base property
// name on SourceType or the Name of another declaration, which produces
// multi-level transitive derivation.
type Declaration struct {
	// Name is both the index name and the name of the derived property.
	Name string
	// DownstreamType is the type whose instances carry derived entries.
	DownstreamType string
	// LinkType is the link traversed to reach the value-providing instance.
	LinkType string
	// SourceType is the expected type of the linked instance.
	SourceType string
	// SourceProperty is a base property or another declaration Name.
	SourceProperty string
	// RequireUnique reports a NotIndexable state when more than one link of
	// LinkType leaves the downstream instance.
	RequireUnique bool
}

// EntryState classifies a derived index entry.
type EntryState int

const (
	// StateIndexed means Keys holds the current derived key(s).
	StateIndexed EntryState = iota
	// StateNoLink: no link of the declared type leaves the instance.
	StateNoLink
	// StateNotUnique: multiple links while RequireUnique is set.
	StateNotUnique
	// StateNoValue: the linked instance does not provide the property.
	StateNoValue
	// StateSourceGone: the linked source instance was deleted.
	StateSourceGone
)

func (s EntryState) String() string {
	switch s {
	case StateIndexed:
		return "INDEXED"
	case StateNoLink:
		return "NOT_INDEXABLE_NO_LINK"
	case StateNotUnique:
		return "NOT_INDEXABLE_NOT_UNIQUE"
	case StateNoValue:
		return "NOT_INDEXABLE_NO_VALUE"
	case StateSourceGone:
		return "NOT_INDEXABLE_SOURCE_GONE"
	default:
		return "UNKNOWN"
	}
}

// Indexable reports whether the entry participates in key lookups.
func (s EntryState) Indexable() bool { return s == StateIndexed }

// Entry is one downstream instance's derived-index state for one declaration.
type Entry struct {
	Declaration string
	ObjectID    string
	State       EntryState
	// Keys holds one key for unique indexes; non-unique indexes may hold
	// several (one per distinct resolved link target).
	Keys []Value
}

// linkKey identifies a link triple.
type linkKey struct {
	from string
	typ  string
	to   string
}

// ChangeRecord is one logged processing unit.
type ChangeRecord struct {
	// Op is the human-readable input operation.
	Op string
	// Input is the structured operation input.
	Input any
	// Affected is the exact, deduplicated set of downstream instances whose
	// derived entries were touched by the processing unit.
	Affected []string
	// Basis explains why the affected set was exactly this set.
	Basis string
	// Entries is the resulting entry state of every affected instance.
	Entries []Entry
	// RolledBack is set when the processing unit aborted atomically.
	RolledBack bool
	// Err is the classified error when the unit failed.
	Err *IndexError
}
