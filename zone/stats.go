package zone

// Stats is the zone-map summary of one row group. Nulls are never part of
// Min/Max. HasMin is false exactly when the group held no non-null value
// (the all-null group), so the absence of a minimum stays distinguishable
// from a minimum that happens to be zero.
type Stats struct {
	Rows      int
	Nulls     int
	HasMin    bool
	IntMin    int64
	IntMax    int64
	BytesMin  []byte
	BytesMax  []byte
}

// FromInts builds stats for a row group. A nil element marks a null.
func FromInts(rows []*int64) Stats {
	s := Stats{Rows: len(rows)}
	for _, p := range rows {
		if p == nil {
			s.Nulls++
			continue
		}
		v := *p
		if !s.HasMin {
			s.HasMin = true
			s.IntMin, s.IntMax = v, v
			continue
		}
		if v < s.IntMin {
			s.IntMin = v
		}
		if v > s.IntMax {
			s.IntMax = v
		}
	}
	return s
}

// FromBytes builds stats for a string row group. A nil element marks a
// null; an empty (non-nil) slice is the real empty string and participates
// in min/max.
func FromBytes(rows [][]byte) Stats {
	s := Stats{Rows: len(rows)}
	for _, v := range rows {
		if v == nil {
			s.Nulls++
			continue
		}
		if !s.HasMin {
			s.HasMin = true
			s.BytesMin = append([]byte(nil), v...)
			s.BytesMax = append([]byte(nil), v...)
			continue
		}
		if string(v) < string(s.BytesMin) {
			s.BytesMin = append(s.BytesMin[:0], v...)
		}
		if string(v) > string(s.BytesMax) {
			s.BytesMax = append(s.BytesMax[:0], v...)
		}
	}
	return s
}

// MatchInt evaluates a predicate against one decoded int64 cell, where
// null is represented by ok=false. Nulls never satisfy a numeric term;
// they satisfy only OpNull. The conjunction follows three-valued logic:
// a null yields unknown unless a term explicitly rejects it.
func MatchInt(p Predicate, v int64, nonNull bool) bool {
	for _, t := range p.Terms {
		if !matchIntTerm(t, v, nonNull) {
			return false
		}
	}
	return true
}

func matchIntTerm(t Term, v int64, nonNull bool) bool {
	switch t.Op {
	case OpNull:
		return !nonNull
	case OpNotNull:
		return nonNull
	}
	if !nonNull {
		return false
	}
	switch t.Op {
	case OpEq:
		return v == t.Ints[0]
	case OpLt:
	return v < t.Ints[0]
	case OpLe:
		return v <= t.Ints[0]
	case OpGt:
		return v > t.Ints[0]
	case OpGe:
		return v >= t.Ints[0]
	case OpIn:
		for _, x := range t.Ints {
			if v == x {
				return true
			}
		}
		return false
	}
	return false
}

// MatchBytes evaluates a predicate against a decoded string cell.
func MatchBytes(p Predicate, v []byte, nonNull bool) bool {
	for _, t := range p.Terms {
		if !matchBytesTerm(t, v, nonNull) {
			return false
		}
	}
	return true
}

func matchBytesTerm(t Term, v []byte, nonNull bool) bool {
	switch t.Op {
	case OpNull:
		return !nonNull
	case OpNotNull:
		return nonNull
	case OpEq:
		return nonNull && string(v) == string(t.Bytes[0])
	case OpIn:
		if !nonNull {
			return false
		}
		s := string(v)
		for _, x := range t.Bytes {
			if s == string(x) {
				return true
			}
		}
		return false
	}
	return false
}
