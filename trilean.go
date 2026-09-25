// Package ontology implements a three-valued logic evaluator for
// filter predicates over entity property sets.
package ontology

// Trilean is a three-valued logic value under Kleene semantics.
type Trilean int

const (
	// False is the definite false value.
	False Trilean = iota
	// True is the definite true value.
	True
	// Unknown represents a missing or undecidable value.
	Unknown
)

// String returns a human-readable name of the value.
func (t Trilean) String() string {
	switch t {
	case False:
		return "False"
	case True:
		return "True"
	case Unknown:
		return "Unknown"
	default:
		return "Invalid"
	}
}

// And returns the Kleene conjunction of a and b.
func And(a, b Trilean) Trilean {
	if a == False || b == False {
		return False
	}
	if a == True && b == True {
		return True
	}
	return Unknown
}

// Or returns the Kleene disjunction of a and b.
func Or(a, b Trilean) Trilean {
	if a == True || b == True {
		return True
	}
	if a == False && b == False {
		return False
	}
	return Unknown
}

// Not returns the Kleene negation of a.
func Not(a Trilean) Trilean {
	switch a {
	case True:
		return False
	case False:
		return True
	default:
		return Unknown
	}
}
