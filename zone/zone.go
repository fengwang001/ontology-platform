// Package zone holds per-row-group statistics (min/max/null count)
// and the predicate pruning logic used to skip row groups that
// cannot possibly match a query.
package zone

// Op is a comparison or null-test operator.
type Op int

const (
	Eq Op = iota
	Lt
	Le
	Gt
	Ge
	In
	IsNull
	IsNotNull
)

// Pred is a single predicate. Vals holds the comparison operands:
// one value for Eq/Lt/Le/Gt/Ge, the candidate set for In, and none
// for IsNull/IsNotNull.
type Pred struct {
	Op   Op
	Vals []int64
}

// Equal builds a `= v` predicate.
func Equal(v int64) Pred { return Pred{Op: Eq, Vals: []int64{v}} }

// LessThan builds a `< v` predicate.
func LessThan(v int64) Pred { return Pred{Op: Lt, Vals: []int64{v}} }

// LessEqual builds a `<= v` predicate.
func LessEqual(v int64) Pred { return Pred{Op: Le, Vals: []int64{v}} }

// GreaterThan builds a `> v` predicate.
func GreaterThan(v int64) Pred { return Pred{Op: Gt, Vals: []int64{v}} }

// GreaterEqual builds a `>= v` predicate.
func GreaterEqual(v int64) Pred { return Pred{Op: Ge, Vals: []int64{v}} }

// InSet builds an `IN (vs...)` predicate.
func InSet(vs ...int64) Pred { return Pred{Op: In, Vals: vs} }

// Null builds an `IS NULL` predicate.
func Null() Pred { return Pred{Op: IsNull} }

// NotNull builds an `IS NOT NULL` predicate.
func NotNull() Pred { return Pred{Op: IsNotNull} }

// Stats summarizes one row group. Min/Max are meaningful only when
// HasMinMax is true; an all-null group has HasMinMax == false, so
// "no min/max" is never confused with a zero value.
type Stats struct {
	Rows      int
	Nulls     int
	Min       int64
	Max       int64
	HasMinMax bool
}

// Add folds one value into the statistics. Null values update only
// the null counter and never the min/max.
func (s *Stats) Add(v int64, isNull bool) {
	s.Rows++
	if isNull {
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
