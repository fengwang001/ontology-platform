// Package api is the public entry point to the incremental L EXCEPT ALL R
// materialized view. Changes are applied in ordered all-or-nothing batches;
// readers only ever observe batch-boundary states.
package api

import (
	"errors"
	"sync"

	"ontology/exc"
)

// Re-exported operator types and sentinels.
type (
	Side   = exc.Side
	Change = exc.Change
	Out    = exc.Out
)

const (
	L = exc.L
	R = exc.R
)

var (
	// ErrInvalidChange: empty row, zero delta, or side not L/R.
	ErrInvalidChange = exc.ErrInvalidChange
	// ErrUnderflow: a delete would drive a side's row multiplicity negative.
	ErrUnderflow = exc.ErrUnderflow
	// ErrTooManyRows: the change would exceed the distinct-row union cap.
	ErrTooManyRows = exc.ErrTooManyRows
)

// View is the materialized view, safe for concurrent use.
type View struct {
	mu sync.RWMutex
	op *exc.Op
}

// New returns a view capped at maxRows distinct rows live on either side.
func New(maxRows int) *View { return &View{op: exc.New(maxRows)} }

// Apply processes a batch strictly in order. If any entry is rejected the
// whole batch is rolled back: counts, view and emitted log are unchanged.
func (v *View) Apply(chs []Change) ([]Out, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	outs := make([]Out, 0, len(chs))
	for i := range chs {
		out, emit, err := v.op.Apply(chs[i])
		if err != nil {
			for j := i - 1; j >= 0; j-- {
				// Replay inverses in reverse order; deletions trace a path
				// already proven non-negative, inserts never re-hit the cap.
				inv := chs[j]
				inv.Delta = -inv.Delta
				_, _, _ = v.op.Apply(inv)
			}
			return nil, err
		}
		if emit {
			outs = append(outs, out)
		}
	}
	return outs, nil
}

// View returns a copy of the materialized view (zero rows omitted).
func (v *View) View() map[string]int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.op.View()
}

// SelfCheck replays built-in change sequences and verifies the four
// invariants (prefix/batch recompute agreement, non-negativity, minimal
// changelog, no trace on rejection). It uses no shared state.
func SelfCheck() error {
	v := New(100)
	seq := []Change{
		{Side: R, Row: "a", Delta: 1}, {Side: L, Row: "a", Delta: 1},
		{Side: L, Row: "a", Delta: 1}, {Side: L, Row: "a", Delta: 2},
		{Side: R, Row: "a", Delta: 1}, {Side: R, Row: "a", Delta: 3},
		{Side: L, Row: "a", Delta: -1}, {Side: R, Row: "a", Delta: -4},
		{Side: L, Row: "b", Delta: 1}, {Side: R, Row: "b", Delta: 1},
	}
	l := map[string]int{}
	r := map[string]int{}
	for _, c := range seq {
		outs, err := v.Apply([]Change{c})
		if err != nil {
			return err
		}
		if c.Side == L {
			l[c.Row] += c.Delta
		} else {
			r[c.Row] += c.Delta
		}
		if !equalView(v.View(), recompute(l, r)) {
			return errors.New("SelfCheck: prefix disagrees with batch recompute")
		}
		if len(outs) > 1 || (len(outs) == 1 && abs(outs[0].Delta) > abs(c.Delta)) {
			return errors.New("SelfCheck: changelog not minimal")
		}
		for _, n := range v.View() {
			if n <= 0 {
				return errors.New("SelfCheck: negative or zero multiplicity")
			}
		}
	}
	before := v.View()
	if _, err := v.Apply([]Change{{Side: L, Row: "a", Delta: -100}}); !errors.Is(err, ErrUnderflow) {
		return errors.New("SelfCheck: expected underflow")
	}
	if !equalView(v.View(), before) {
		return errors.New("SelfCheck: rejected batch left a trace")
	}
	return nil
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// recompute is the independent batch reference: max(0, cntL-cntR) per row.
func recompute(l, r map[string]int) map[string]int {
	want := map[string]int{}
	for x, n := range l {
		if d := n - r[x]; d > 0 {
			want[x] = d
		}
	}
	return want
}

func equalView(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
