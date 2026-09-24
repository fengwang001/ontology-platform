// Package exc is the incremental operator for L EXCEPT ALL: out=max(0,cntL-cntR).
package exc

import (
	"errors"
	"strconv"

	"ontology/mset"
)

// Side selects one input table; its zero value is deliberately invalid.
type Side uint8

const (
	Left  Side = 1
	Right Side = 2 // any other value is rejected by package api
)

// Change is one upstream change; Delta non-zero: + inserts, - deletes.
type Change struct {
	Row   string
	Side  Side
	Delta int
}
type Out struct {
	Row   string
	Delta int
}

var (
	ErrTooManyRows = errors.New("exc: distinct row limit exceeded")
	ErrUnderflow   = mset.ErrUnderflow
)

// Operator is the stateful EXCEPT ALL operator.
type Operator struct {
	maxRows int
	l, r    *mset.Multiset
	view    map[string]int
	union   int // distinct rows non-zero on at least one side
	// touched: row-state entries read/written handling the last single change
	// (each side multiplicity entry and the view entry count 1). Never exported.
	touched int
}

func New(maxRows int) *Operator {
	return &Operator{maxRows: maxRows, l: mset.New(), r: mset.New(), view: map[string]int{}}
}

// View returns an independent snapshot (zero rows absent, values positive).
func (o *Operator) View() map[string]int {
	out := make(map[string]int, len(o.view))
	for row, n := range o.view {
		out[row] = n
	}
	return out
}

// Apply processes a batch in order, emitting one Out per entry whose output
// multiplicity changed. Any rejection rolls back all committed entries.
func (o *Operator) Apply(chs []Change) ([]Out, error) {
	type rec struct {
		c      Change
		oldOut int
		ud     int // union-counter delta (+1/-1/0)
	}
	done := make([]rec, 0, len(chs))
	rollback := func() {
		for i := len(done) - 1; i >= 0; i-- {
			d := done[i]
			m := o.r
			if d.c.Side == Left {
				m = o.l
			}
			_ = m.Add(d.c.Row, -d.c.Delta) // exact inverse, cannot underflow
			o.union -= d.ud
			if d.oldOut != 0 {
				o.view[d.c.Row] = d.oldOut
			} else {
				delete(o.view, d.c.Row)
			}
		}
	}
	fail := func(err error) ([]Out, error) { o.touched = 3; rollback(); return nil, err }
	outs := make([]Out, 0, len(chs))
	for _, c := range chs {
		m, other := o.l, o.r
		if c.Side == Right {
			m, other = o.r, o.l
		}
		oldM, oldOther, oldOut := m.Get(c.Row), other.Get(c.Row), o.view[c.Row]
		newM := oldM + c.Delta
		if newM < 0 {
			return fail(ErrUnderflow)
		}
		was, will := oldM > 0 || oldOther > 0, newM > 0 || oldOther > 0
		ud := 0
		if will && !was {
			ud = 1
		} else if was && !will {
			ud = -1
		}
		if ud == 1 && o.union >= o.maxRows {
			return fail(ErrTooManyRows)
		}
		if err := m.Add(c.Row, c.Delta); err != nil { // cannot fail here
			return fail(err)
		}
		o.union += ud
		diff := newM - oldOther
		if c.Side == Right {
			diff = oldOther - newM
		}
		if diff < 0 {
			diff = 0
		}
		if diff != 0 {
			o.view[c.Row] = diff
		} else {
			delete(o.view, c.Row)
		}
		o.touched = 5 // 3 reads + multiplicity entry + view entry written
		done = append(done, rec{c, oldOut, ud})
		if d := diff - oldOut; d != 0 {
			outs = append(outs, Out{c.Row, d})
		}
	}
	return outs, nil
}

// CheckIncrementalCost returns whether one change after loading m in
// {100,1000,10000} distinct rows touches <= 6 entries; the count never leaves
// the package.
func CheckIncrementalCost() bool {
	for _, m := range []int{100, 1000, 10000} {
		o := New(2*m + 1)
		chs := make([]Change, 0, 2*m)
		for i := 0; i < m; i++ {
			x := "r" + strconv.Itoa(i)
			chs = append(chs, Change{x, Left, 1}, Change{x, Right, 1})
		}
		if _, err := o.Apply(chs); err != nil {
			return false
		}
		if _, err := o.Apply([]Change{{"r0", Left, 1}}); err != nil || o.touched > 6 {
			return false
		}
	}
	return true
}
