// Package ontology implements a three-valued (Kleene) logic evaluator
// for filter predicate trees over entity attribute maps.
package ontology

// Tri is a three-valued logic truth value.
type Tri uint8

const (
	// False means the predicate definitely does not hold.
	False Tri = iota
	// True means the predicate definitely holds.
	True
	// Unknown means the predicate cannot be decided (e.g. missing
	// attribute or NaN comparison).
	Unknown
)

func (t Tri) String() string {
	switch t {
	case False:
		return "False"
	case True:
		return "True"
	case Unknown:
		return "Unknown"
	default:
		return "Tri(?)"
	}
}

// And returns the Kleene conjunction of a and b. It never short-circuits;
// short-circuiting is a property of tree evaluation, not of the algebra.
func And(a, b Tri) Tri {
	if a == False || b == False {
		return False
	}
	if a == True && b == True {
		return True
	}
	return Unknown
}

// Or returns the Kleene disjunction of a and b.
func Or(a, b Tri) Tri {
	if a == True || b == True {
		return True
	}
	if a == False && b == False {
		return False
	}
	return Unknown
}

// Not returns the Kleene negation of a.
func Not(a Tri) Tri {
	switch a {
	case True:
		return False
	case False:
		return True
	default:
		return Unknown
	}
}
