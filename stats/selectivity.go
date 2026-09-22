package stats

// Estimate is a selectivity estimate plus a reliability flag. Reliable is
// false when the hard-coded default had to be used (missing statistics).
type Estimate struct {
	Sel      float64
	Reliable bool
}

// EqSelectivity estimates the selectivity of an equality predicate
// left.lcol = right.rcol as 1/max(NDV_l, NDV_r). Missing column stats fall
// back to DefaultSelectivity and mark the estimate unreliable.
func EqSelectivity(left *Table, lcol string, right *Table, rcol string) Estimate {
	lc, lok := columnOf(left, lcol)
	rc, rok := columnOf(right, rcol)
	if !lok || !rok {
		return Estimate{Sel: DefaultSelectivity, Reliable: false}
	}
	ndv := lc.NDV
	if rc.NDV > ndv {
		ndv = rc.NDV
	}
	if ndv < 1 {
		ndv = 1
	}
	return Estimate{Sel: 1 / ndv, Reliable: true}
}

func columnOf(t *Table, col string) (Column, bool) {
	if t == nil {
		return Column{}, false
	}
	c, ok := t.Columns[col]
	return c, ok
}
