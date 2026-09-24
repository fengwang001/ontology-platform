// Package exc is the incremental EXCEPT ALL operator: it holds both input
// multisets and turns each single input change into at most one output
// changelog entry for the materialized view of L EXCEPT ALL R.
package exc

import (
	"errors"
	"fmt"

	"ontology/mset"
)

var (
	// ErrInvalidChange: empty row, zero delta, or side not L/R.
	ErrInvalidChange = errors.New("exc: invalid change")
	// ErrUnderflow: a delete would drive a side's row multiplicity negative.
	ErrUnderflow = errors.New("exc: delete underflow")
	// ErrTooManyRows: distinct rows live on either side would exceed the cap.
	ErrTooManyRows = errors.New("exc: too many distinct rows")
)

// Side identifies the input table a change applies to.
type Side int

const (
	L Side = iota
	R
)

// Change is one upstream delta: Delta copies inserted (positive) or deleted.
type Change struct {
	Side  Side
	Row   string
	Delta int
}

// Out is one changelog entry: add Delta to Row's view multiplicity.
type Out struct {
	Row   string
	Delta int
}

// maxTouches bounds the row-state entries one change may read or write
// (left count, right count, view entry — never a full-table scan).
const maxTouches = 6

// Op is the incremental operator. Not safe for concurrent use; callers
// serialize (api does).
type Op struct {
	l, r    *mset.MSet
	view    map[string]int
	rows    int // distinct rows with non-zero count on either side
	maxRows int
	touched int // entries read/written while processing the last change
}

// New returns an operator whose two sides may hold at most maxRows
// distinct live rows in union.
func New(maxRows int) *Op {
	return &Op{l: mset.New(maxRows), r: mset.New(maxRows), view: map[string]int{}, maxRows: maxRows}
}

// Apply processes one change. On error nothing is mutated. When the row's
// output multiplicity changes it returns the single changelog entry.
func (o *Op) Apply(c Change) (Out, bool, error) {
	o.touched = 0
	if c.Row == "" || c.Delta == 0 || (c.Side != L && c.Side != R) {
		return Out{}, false, ErrInvalidChange
	}
	cl, cr := o.l.Get(c.Row), o.r.Get(c.Row)
	o.touched += 2
	nl, nr := cl, cr
	if c.Side == L {
		nl += c.Delta
	} else {
		nr += c.Delta
	}
	// All validation happens before any mutation: underflow and the
	// distinct-row union cap both reject without leaving a trace.
	if nl < 0 || nr < 0 {
		return Out{}, false, ErrUnderflow
	}
	if cl == 0 && cr == 0 && (nl > 0 || nr > 0) && o.rows >= o.maxRows {
		return Out{}, false, ErrTooManyRows
	}
	oldOut, newOut := max(0, cl-cr), max(0, nl-nr)
	src := o.l
	if c.Side == R {
		src = o.r
	}
	if err := src.Add(c.Row, c.Delta); err != nil {
		return Out{}, false, mapErr(err) // defensive; already pre-checked
	}
	o.touched++
	switch {
	case cl == 0 && cr == 0:
		o.rows++
	case nl == 0 && nr == 0:
		o.rows--
	}
	if newOut == oldOut {
		return Out{}, false, nil
	}
	if newOut == 0 {
		delete(o.view, c.Row)
	} else {
		o.view[c.Row] = newOut
	}
	o.touched++
	return Out{Row: c.Row, Delta: newOut - oldOut}, true, nil
}

func mapErr(err error) error {
	switch {
	case errors.Is(err, mset.ErrUnderflow):
		return ErrUnderflow
	case errors.Is(err, mset.ErrTooManyRows):
		return ErrTooManyRows
	default:
		return fmt.Errorf("exc: %w", err)
	}
}

// View returns a copy of the current materialized view.
func (o *Op) View() map[string]int {
	cp := make(map[string]int, len(o.view))
	for k, v := range o.view {
		cp[k] = v
	}
	return cp
}
