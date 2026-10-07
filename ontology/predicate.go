package ontology

// CmpOp is an ordered-comparison operator for row-level predicates.
type CmpOp string

const (
	OpEq CmpOp = "eq"
	OpNe CmpOp = "ne"
	OpLt CmpOp = "lt"
	OpLe CmpOp = "le"
	OpGt CmpOp = "gt"
	OpGe CmpOp = "ge"
)

// Atom is one row-level predicate atom. It is always evaluated against the
// raw stored value of Property, regardless of the subject's read permission
// or any masking rule.
//
// Semantics by the declared type of Property:
//   - string/boolean: only OpEq and OpNe are valid; comparison is on
//     Value.Str / Value.Bool.
//   - int/float: OpEq..OpGe are valid; In tests membership in Numeric.
//   - string: In tests membership in Members.
//
// An atom whose property is absent from the instance evaluates to false.
type Atom struct {
	Property string
	Op       CmpOp
	Str      string
	Bool     bool
	Numeric  float64
	InStr    []string
	InNum    []float64
}

// Predicate is a conjunction (AND) of atoms. Row-level policies that need OR
// semantics register several policies.
type Predicate struct {
	Atoms []Atom
}

func (p Predicate) eval(raw map[string]Value, types map[string]DeclaredType) bool {
	for _, atom := range p.Atoms {
		if !atom.eval(raw, types) {
			return false
		}
	}
	return true
}

func (a Atom) eval(raw map[string]Value, types map[string]DeclaredType) bool {
	value, present := raw[a.Property]
	if !present {
		return false
	}
	dt := types[a.Property]
	switch dt.Kind {
	case KindString:
		if a.Op == "in" {
			for _, m := range a.InStr {
				if value.Str == m {
					return true
				}
			}
			return false
		}
		switch a.Op {
		case OpEq:
			return value.Str == a.Str
		case OpNe:
			return value.Str != a.Str
		default:
			return false
		}
	case KindBoolean:
		switch a.Op {
		case OpEq:
			return value.Bool == a.Bool
		case OpNe:
			return value.Bool != a.Bool
		default:
			return false
		}
	default: // numeric
		got, ok := value.asFloat(dt.Kind)
		if !ok {
			return false
		}
		if a.Op == "in" {
			for _, m := range a.InNum {
				if got == m {
					return true
				}
			}
			return false
		}
		switch a.Op {
		case OpEq:
			return got == a.Numeric
		case OpNe:
			return got != a.Numeric
		case OpLt:
			return got < a.Numeric
		case OpLe:
			return got <= a.Numeric
		case OpGt:
			return got > a.Numeric
		case OpGe:
			return got >= a.Numeric
		default:
			return false
		}
	}
}
