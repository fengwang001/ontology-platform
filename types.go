package unique

// NullableMode selects NULL semantics for a nullable unique constraint.
type NullableMode int

const (
	// NullsDistinct follows SQL semantics: NULL conflicts with nothing.
	NullsDistinct NullableMode = iota
	// NullsEqual treats two NULL values as equal and therefore conflicting.
	NullsEqual
)

// Constraint declares a composite unique constraint over property names.
type Constraint struct {
	Name     string
	Cols     []string
	Nullable NullableMode
}

// Record is one stored object addressed by its primary key ID.
type Record struct {
	ID    string
	Props map[string]string
}

// present distinguishes NULL (missing key) from the empty string.
type present struct {
	value   string
	missing bool
}

func isNull(p present) bool { return p.missing }
