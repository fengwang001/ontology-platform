// Package zone defines row-group statistics (min/max/null count) and
// predicate pruning. Pruning is conservative: MayMatch=false means the
// group provably contains no matching row, never the other way round.
package zone

// Stats summarizes one row group. Min/Max cover non-null values only
// and are valid only when HasMinMax is true; an all-null group reports
// HasMinMax=false rather than zero values.
type Stats struct {
	Rows      int
	Nulls     int
	Min       int64
	Max       int64
	HasMinMax bool
}

// Add folds one row into the statistics.
func (s *Stats) Add(v int64, null bool) {
	s.Rows++
	if null {
		s.Nulls++
		return
	}
	if !s.HasMinMax {
		s.Min, s.Max, s.HasMinMax = v, v, true
		return
	}
	if v < s.Min {
		s.Min = v
	}
	if v > s.Max {
		s.Max = v
	}
}

// Pred is a predicate usable both for row-level matching and for
// statistics-based pruning. Null rows never reach Match: a comparison
// against null is neither true nor false, so nulls are routed to
// MatchNull instead.
type Pred interface {
	Match(v int64) bool // called only for non-null values
	MatchNull() bool    // whether a null row matches
	MayMatch(s Stats) bool
}

// Op is a comparison operator.
type Op int

const (
	Eq Op = iota
	Lt
	Le
	Gt
	Ge
)

type cmp struct {
	op Op
	v  int64
}

// Cmp returns the predicate "column <op> v".
func Cmp(op Op, v int64) Pred { return cmp{op, v} }

func (c cmp) Match(v int64) bool {
	switch c.op {
	case Eq:
		return v == c.v
	case Lt:
		return v < c.v
	case Le:
		return v <= c.v
	case Gt:
		return v > c.v
	case Ge:
		return v >= c.v
	}
	return false
}

func (c cmp) MatchNull() bool { return false }

func (c cmp) MayMatch(s Stats) bool {
	if !s.HasMinMax {
		return false
	}
	switch c.op {
	case Eq:
		return c.v >= s.Min && c.v <= s.Max
	case Lt:
		return s.Min < c.v
	case Le:
		return s.Min <= c.v
	case Gt:
		return s.Max > c.v
	case Ge:
		return s.Max >= c.v
	}
	return false
}

type inSet struct{ vals []int64 }

// In returns the predicate "column IN (vals...)".
func In(vals ...int64) Pred {
	cp := make([]int64, len(vals))
	copy(cp, vals)
	return inSet{cp}
}

func (p inSet) Match(v int64) bool {
	for _, x := range p.vals {
		if v == x {
			return true
		}
	}
	return false
}

func (p inSet) MatchNull() bool { return false }

func (p inSet) MayMatch(s Stats) bool {
	if !s.HasMinMax {
		return false
	}
	for _, x := range p.vals {
		if x >= s.Min && x <= s.Max {
			return true
		}
	}
	return false
}

type nullPred struct{ wantNull bool }

// IsNull returns the predicate "column IS NULL".
func IsNull() Pred { return nullPred{true} }

// IsNotNull returns the predicate "column IS NOT NULL".
func IsNotNull() Pred { return nullPred{false} }

func (p nullPred) Match(v int64) bool { return !p.wantNull }
func (p nullPred) MatchNull() bool    { return p.wantNull }

func (p nullPred) MayMatch(s Stats) bool {
	if p.wantNull {
		return s.Nulls > 0
	}
	return s.Rows > s.Nulls
}

type and struct{ ps []Pred }

// And returns the conjunction of ps.
func And(ps ...Pred) Pred { return and{ps} }

func (a and) Match(v int64) bool {
	for _, p := range a.ps {
		if !p.Match(v) {
			return false
		}
	}
	return true
}

func (a and) MatchNull() bool {
	for _, p := range a.ps {
		if !p.MatchNull() {
			return false
		}
	}
	return true
}

func (a and) MayMatch(s Stats) bool {
	for _, p := range a.ps {
		if !p.MayMatch(s) {
			return false
		}
	}
	return true
}
