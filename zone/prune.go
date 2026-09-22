package zone

// CanSkip reports whether every row in a group is provably rejected by p.
// The rule is conservative: a term may skip the group only when all
// (possibly null) rows are impossible matches; when unsure it keeps the
// group. The conjunction may skip only when one single term already does,
// which is sound because a row must satisfy every term.
func CanSkip(s Stats, p Predicate) bool {
	for _, t := range p.Terms {
		if termSkips(s, t) {
			return true
		}
	}
	return false
}

func termSkips(s Stats, t Term) bool {
	switch t.Op {
	case OpNull:
		return s.Nulls == 0
	case OpNotNull:
		return s.Nulls == s.Rows
	}
	// Every value comparison term excludes null rows already; it can skip
	// the whole group only when some rows exist and all of them are null.
	if !s.HasMin {
		return true
	}
	switch t.Kind {
	case KindBytes:
		return bytesTermSkips(s, t)
	default:
		return intTermSkips(s, t)
	}
}

func intTermSkips(s Stats, t Term) bool {
	lo, hi := s.IntMin, s.IntMax
	switch t.Op {
	case OpEq:
		v := t.Ints[0]
		return v < lo || v > hi
	case OpLt: // matches v < x; possible iff lo < x
		return !(lo < t.Ints[0])
	case OpLe: // matches v <= x; possible iff lo <= x
		return !(lo <= t.Ints[0])
	case OpGt: // matches v > x; possible iff hi > x
		return !(hi > t.Ints[0])
	case OpGe: // matches v >= x; possible iff hi >= x
		return !(hi >= t.Ints[0])
	case OpIn:
		for _, v := range t.Ints {
			if v >= lo && v <= hi {
				return false
			}
		}
		return true
	}
	return false
}

func bytesTermSkips(s Stats, t Term) bool {
	lo, hi := string(s.BytesMin), string(s.BytesMax)
	switch t.Op {
	case OpEq:
		v := string(t.Bytes[0])
		return v < lo || v > hi
	case OpIn:
		for _, v := range t.Bytes {
			sv := string(v)
			if sv >= lo && sv <= hi {
				return false
			}
		}
		return true
	}
	// Unsupported comparisons on strings are not proven to skip.
	return false
}
