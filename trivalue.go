package ontology

// Tri is a three-valued logic truth value: True, False or Unknown.
type Tri uint8

const (
	Unknown Tri = iota
	True
	False
)

// String renders the value for diagnostics.
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

// kleeneAnd returns the Kleene conjunction: False dominates, otherwise
// Unknown dominates over True.
func kleeneAnd(a, b Tri) Tri {
	if a == False || b == False {
		return False
	}
	if a == Unknown || b == Unknown {
		return Unknown
	}
	return True
}

// kleeneOr returns the Kleene disjunction: True dominates, otherwise
// Unknown dominates over False.
func kleeneOr(a, b Tri) Tri {
	if a == True || b == True {
		return True
	}
	if a == Unknown || b == Unknown {
		return Unknown
	}
	return False
}

// kleeneNot preserves Unknown: Not(Unknown) == Unknown.
func kleeneNot(a Tri) Tri {
	switch a {
	case True:
		return False
	case False:
	return True
	default:
		return Unknown
	}
}
