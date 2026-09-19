package predicate

// Tri is the three-valued logic result of predicate evaluation.
type Tri uint8

const (
	// Unknown means the truth value cannot be determined (e.g. a missing
	// attribute was compared).
	Unknown Tri = iota
	// True is the determinate truth value.
	True
	// False is the determinate false value.
	False
)

// String renders the tri-state value.
func (t Tri) String() string {
	switch t {
	case True:
		return "True"
	case False:
		return "False"
	default:
		return "Unknown"
	}
}

// KleeneNot is the three-valued negation: Not(Unknown) == Unknown.
func KleeneNot(t Tri) Tri {
	switch t {
	case True:
		return False
	case False:
		return True
	default:
		return Unknown
	}
}
