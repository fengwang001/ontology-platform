// Package inter maintains the per-value intersection view over relations
// L and R. Values are located by hash lookup; it depends only on mset.
package inter

import (
	"errors"
	"sync"

	"ontology/mset"
)

// ErrEmptyVal is returned when val is the empty string.
var ErrEmptyVal = errors.New("inter: value must not be empty")

// View is the materialized INTERSECT ALL view keyed by value.
type View struct {
	mu    sync.RWMutex
	cells map[string]*mset.Cell
	// probed is the number of distinct value entries inspected by the most
	// recent Apply. It is unexported and never exposed through the public API.
	probed int
}

// New returns an empty View.
func New() *View {
	return &View{cells: make(map[string]*mset.Cell)}
}

// Apply adds d to side's count of val and returns the signed change of the
// intersection multiplicity. NULL counts are tracked but never match, so
// their delta is always 0. A rejected call changes no state at all.
func (v *View) Apply(side mset.Side, val string, d int) (int, error) {
	if val == "" {
		return 0, ErrEmptyVal
	}
	if d == 0 {
		return 0, mset.ErrZeroDelta
	}
	if side != mset.L && side != mset.R {
		return 0, mset.ErrBadSide
	}
	v.mu.Lock()
	defer v.mu.Unlock()

	v.probed = 0
	c, ok := v.cells[val] // hash lookup: exactly one value entry is examined
	v.probed++
	l, r := 0, 0
	if ok {
		l, r = c.Counts()
	}
	cur := l
	if side == mset.R {
		cur = r
	}
	if cur+d < 0 {
		return 0, mset.ErrCountNegative // before any insertion or mutation
	}
	if !ok {
		c = &mset.Cell{}
		v.cells[val] = c
	}
	delta, err := c.Apply(side, d)
	if err != nil {
		return 0, err
	}
	if mset.IsNull(val) {
		return 0, nil // NULL never participates in matching
	}
	return delta, nil
}

// View returns a snapshot of intersection multiplicities, restricted to
// values currently present in the intersection (m > 0). NULL is absent.
func (v *View) View() map[string]int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := make(map[string]int, len(v.cells))
	for val, c := range v.cells {
		if mset.IsNull(val) {
			continue
		}
		if m := c.Mult(); m > 0 {
			out[val] = m
		}
	}
	return out
}
