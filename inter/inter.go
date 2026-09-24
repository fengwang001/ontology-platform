// Package inter manages per-value intersection state: it routes each Apply
// to the value's own mset.Counter and materializes the view of multiplicities.
package inter

import (
	"errors"
	"sync"

	"ontology/mset"
)

// ErrEmptyVal rejects Apply with an empty value string.
var ErrEmptyVal = errors.New("inter: value must be non-empty")

// Change is one changelog entry: Val's intersection multiplicity moved by
// exactly Delta (+1 or -1).
type Change struct {
	Val   string
	Delta int
}

// Table maps each value to its two-side counter.
type Table struct {
	mu      sync.RWMutex
	byVal   map[string]*mset.Counter
	checked int // values inspected by the most recent Apply (never exported)
}

// New returns an empty Table.
func New() *Table { return &Table{byVal: make(map[string]*mset.Counter)} }

// Apply updates one value on one side and returns the changelog entries
// (one per ±1 crossing of the intersection multiplicity). Any rejection
// leaves the table untouched.
func (t *Table) Apply(s mset.Side, val string, d int) ([]Change, error) {
	if val == "" {
		return nil, ErrEmptyVal
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	c, ok := t.byVal[val]
	t.checked = 1 // hash lookup touches exactly one value
	if !ok {
		c = mset.New(val == mset.Null)
	}
	delta, err := c.Apply(s, d)
	if err != nil {
		return nil, err // counter rejected before mutating: no trace
	}
	if c.Empty() {
		delete(t.byVal, val)
	} else {
		t.byVal[val] = c
	}
	out := make([]Change, 0, abs(delta))
	for i := 0; i < abs(delta); i++ {
		out = append(out, Change{Val: val, Delta: sign(delta)})
	}
	return out, nil
}

// View returns each live value's intersection multiplicity. NULL, if
// present, always maps to 0.
func (t *Table) View() map[string]int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string]int, len(t.byVal))
	for v, c := range t.byVal {
		out[v] = c.M()
	}
	return out
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func sign(x int) int {
	if x < 0 {
		return -1
	}
	return 1
}
