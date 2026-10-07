package ontology

// Value is a typed attribute value. Exactly one of its fields is meaningful,
// selected by the declared TypeKind.
type Value struct {
	Str  string
	Int  int64
	Real float64
	Bool bool
}

// asFloat returns the numeric view of a value used by ordered predicates.
func (v Value) asFloat(kind TypeKind) (float64, bool) {
	switch kind {
	case KindInt:
		return float64(v.Int), true
	case KindFloat:
		return v.Real, true
	default:
		return 0, false
	}
}

// Conforms reports whether v satisfies the declared type t, including enum and
// range constraints. It is the single place where the object type contract is
// enforced for stored, submitted and masked values alike.
func (v Value) Conforms(t DeclaredType) bool {
	switch t.Kind {
	case KindString:
		if len(t.EnumValues) > 0 {
			matched := false
			for _, allowed := range t.EnumValues {
				if v.Str == allowed {
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		}
		return true
	case KindInt:
		if t.Min != nil && float64(v.Int) < *t.Min {
			return false
		}
		if t.Max != nil && float64(v.Int) > *t.Max {
			return false
		}
		return true
	case KindFloat:
		if t.Min != nil && v.Real < *t.Min {
			return false
		}
		if t.Max != nil && v.Real > *t.Max {
			return false
		}
		return true
	case KindBoolean:
		return true
	default:
		return false
	}
}
