package predicate

// Tri is a Kleene three-valued logic truth value.
type Tri int

const (
	// Unknown is the zero value: missing information.
	Unknown Tri = iota
	True
	False
)

// String renders a Tri value.
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

// KleeneAnd implements AND under Kleene three-valued logic.
// False dominates; True is neutral; otherwise Unknown.
func KleeneAnd(a, b Tri) Tri {
	if a == False || b == False {
		return False
	}
	if a == True && b == True {
		return True
	}
	return Unknown
}

// KleeneOr implements OR under Kleene three-valued logic.
func KleeneOr(a, b Tri) Tri {
	if a == True || b == True {
		return True
	}
	if a == False && b == False {
		return False
	}
	return Unknown
}

// KleeneNot implements NOT under Kleene three-valued logic.
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
