package ontology

// Trilean is a three-valued truth value following Kleene logic.
type Trilean int

const (
	False Trilean = iota
	True
	Unknown
)

func (t Trilean) String() string {
	switch t {
	case True:
		return "True"
	case False:
		return "False"
	default:
		return "Unknown"
	}
}

// And returns the Kleene conjunction of two truth values.
func And(t, u Trilean) Trilean {
	if t == False || u == False {
		return False
	}
	if t == True && u == True {
		return True
	}
	return Unknown
}

// Or returns the Kleene disjunction of two truth values.
func Or(t, u Trilean) Trilean {
	if t == True || u == True {
		return True
	}
	if t == False && u == False {
		return False
	}
	return Unknown
}

// Not returns the Kleene negation of a truth value.
func Not(t Trilean) Trilean {
	switch t {
	case True:
		return False
	case False:
		return True
	default:
		return Unknown
	}
}
