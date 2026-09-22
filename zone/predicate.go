package zone

// Op is a scalar comparison operator.
type Op uint8

const (
	OpEQ Op = iota
	OpLT
	OpLE
	OpGT
	OpGE
)

// Predicate evaluates rows (Match) and row-group stats (CouldHave).
// CouldHave must be a conservative over-approximation: it returns true
// whenever a matching row might exist, false only when no match is possible.
type Predicate interface {
	Match(v Value) bool
	CouldHave(s Stats) bool
}

// Cmp is a numeric/string comparison: v Op RHS. NULL never matches.
type Cmp struct {
	Op  Op
	RHS Value
}

// Match applies the comparison to a single row.
func (c Cmp) Match(v Value) bool {
	if v.Kind == Null || v.Kind != c.RHS.Kind {
		return false
	}
	r := Compare(v, c.RHS)
	switch c.Op {
	case OpEQ:
		return r == 0
	case OpLT:
		return r < 0
	case OpLE:
		return r <= 0
	case OpGT:
		return r > 0
	default:
		return r >= 0
	}
}

// CouldHave performs zone-map pruning against [Min, Max].
func (c Cmp) CouldHave(s Stats) bool {
	if !s.HasNonNull() {
		return false
	}
	if c.RHS.Kind != s.Min.Kind {
		return false
	}
	switch c.Op {
	case OpEQ:
		return s.Contains(c.RHS) // == cannot exclude when RHS equals min/max
	case OpLT:
		return Compare(s.Min, c.RHS) < 0 // some value < RHS iff min < RHS
	case OpLE:
		return Compare(s.Min, c.RHS) <= 0
	case OpGT:
		return Compare(s.Max, c.RHS) > 0 // some value > RHS iff max > RHS
	default: // OpGE
		return Compare(s.Max, c.RHS) >= 0
	}
}

// In matches when v equals one of Items. NULL never matches.
type In struct {
	Items []Value
}

// Match applies membership to one row.
func (in In) Match(v Value) bool {
	if v.Kind == Null {
		return false
	}
	for _, it := range in.Items {
		if v.Equal(it) {
			return true
		}
	}
	return false
}

// CouldHave keeps the group iff at least one item falls inside the zone.
func (in In) CouldHave(s Stats) bool {
	if !s.HasNonNull() {
		return false
	}
	for _, it := range in.Items {
		if it.Kind == s.Min.Kind && s.Contains(it) {
			return true
		}
	}
	return false
}

// IsNull matches exactly NULL rows.
type IsNull struct{}

// Match reports whether the row is null.
func (IsNull) Match(v Value) bool { return v.Kind == Null }

// CouldHave keeps groups that contain at least one null.
func (IsNull) CouldHave(s Stats) bool { return s.Nulls > 0 }

// IsNotNull matches exactly non-null rows.
type IsNotNull struct{}

// Match reports whether the row is non-null.
func (IsNotNull) Match(v Value) bool { return v.Kind != Null }

// CouldHave keeps groups that contain at least one non-null.
func (IsNotNull) CouldHave(s Stats) bool { return s.HasNonNull() }

// And is the conjunction of predicates. A group survives only when every
// conjunct may match; a row matches only when all conjuncts match.
type And struct{ Parts []Predicate }

// Match applies all conjuncts.
func (a And) Match(v Value) bool {
	for _, p := range a.Parts {
		if !p.Match(v) {
			return false
		}
	}
	return true
}

// CouldHave intersects the per-predicate survival sets.
func (a And) CouldHave(s Stats) bool {
	for _, p := range a.Parts {
		if !p.CouldHave(s) {
			return false
		}
	}
	return true
}
