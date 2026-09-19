package ontology

// Tri is the three-valued logic result of predicate evaluation:
// True, False, or Unknown (e.g. a referenced property is missing).
type Tri uint8

const (
	// Unknown means the truth value cannot be determined.
	Unknown Tri = iota
	// True means the predicate holds.
	True
	// False means the predicate does not hold.
	False
)

// String renders the tri-state for diagnostics.
func (t Tri) String() string {
	switch t {
	case True:
		return "true"
	case False:
		return "false"
	default:
		return "unknown"
	}
}

// Bool converts an ordinary bool into a definite Tri.
func Bool(b bool) Tri {
	if b {
		return True
	}
	return False
}
