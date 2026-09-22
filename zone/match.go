package zone

// MayMatch reports whether a row group with these statistics could
// contain at least one row satisfying p. It may only return false
// when a match is provably impossible; it never prunes a group that
// could match.
func (s Stats) MayMatch(p Pred) bool {
	switch p.Op {
	case IsNull:
		return s.Nulls > 0
	case IsNotNull:
		return s.Rows-s.Nulls > 0
	}
	// All remaining operators compare against numbers; a group with
	// no non-null values can never satisfy them.
	if !s.HasMinMax {
		return false
	}
	switch p.Op {
	case Eq:
		v := p.Vals[0]
		return v >= s.Min && v <= s.Max
	case Lt:
		return s.Min < p.Vals[0]
	case Le:
		return s.Min <= p.Vals[0]
	case Gt:
		return s.Max > p.Vals[0]
	case Ge:
		return s.Max >= p.Vals[0]
	case In:
		for _, v := range p.Vals {
			if v >= s.Min && v <= s.Max {
				return true
			}
		}
		return false
	}
	return true
}

// MayMatchAll is the pruning test for an AND of predicates: the
// group survives only if it may match every conjunct.
func MayMatchAll(s Stats, ps []Pred) bool {
	for _, p := range ps {
		if !s.MayMatch(p) {
			return false
		}
	}
	return true
}

// Match evaluates p against a single value. A null value matches
// only IsNull; it matches no numeric predicate and no IsNotNull.
func Match(v int64, isNull bool, p Pred) bool {
	if isNull {
		return p.Op == IsNull
	}
	switch p.Op {
	case Eq:
		return v == p.Vals[0]
	case Lt:
		return v < p.Vals[0]
	case Le:
		return v <= p.Vals[0]
	case Gt:
		return v > p.Vals[0]
	case Ge:
		return v >= p.Vals[0]
	case In:
		for _, c := range p.Vals {
			if v == c {
				return true
			}
		}
		return false
	case IsNull:
		return false
	case IsNotNull:
		return true
	}
	return false
}

// MatchAll evaluates an AND of predicates against a single value.
func MatchAll(v int64, isNull bool, ps []Pred) bool {
	for _, p := range ps {
		if !Match(v, isNull, p) {
			return false
		}
	}
	return true
}
