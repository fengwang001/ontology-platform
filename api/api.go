// Package api is the public face of the incremental LAG view.
package api

import (
	"errors"
	"fmt"
	"maps"
	"sync"

	"ontology/lagview"
)

// Op is one upstream change: insert (Del=false) or delete (Del=true) of a row.
type Op struct {
	Del           bool
	ID, Sort, Val int64
	Part          string
}

// Change is one changelog entry: +(ID,Lag) or -(ID,Lag); nil Lag means NULL.
type Change = lagview.Change

// Distinguishable sentinel errors, one per rejection cause.
var (
	ErrDupID   = errors.New("api: duplicate id")
	ErrNoID    = errors.New("api: no such id")
	ErrBadPart = errors.New("api: empty part")
	ErrTooMany = errors.New("api: row count exceeds maxRows")
)

// API maintains the materialized LAG view; safe for concurrent use.
type API struct {
	mu   sync.RWMutex
	max  int
	lv   *lagview.View
	view map[int64]*int64
}

// New returns an API that holds at most maxRows rows.
func New(maxRows int) *API { return &API{max: maxRows, lv: lagview.New(), view: map[int64]*int64{}} }

// Apply runs a batch in order; any rejection fails the whole batch atomically.
func (a *API) Apply(ops []Op) ([]Change, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.check(ops); err != nil {
		return nil, err
	}
	var out []Change
	for _, op := range ops {
		var cs []Change
		if op.Del {
			cs = a.lv.Delete(op.ID)
		} else {
			cs = a.lv.Insert(op.ID, op.Part, op.Sort, op.Val)
		}
		for _, c := range cs { // the view is maintained by applying our own changelog
			lagview.ApplyChange(a.view, c)
		}
		out = append(out, cs...)
	}
	return out, nil
}

// check validates the whole batch against current state, mutating nothing.
func (a *API) check(ops []Op) error {
	ins, del, n := map[int64]bool{}, map[int64]bool{}, a.lv.Len()
	for _, op := range ops {
		exists := ins[op.ID] || a.lv.Has(op.ID) && !del[op.ID]
		if op.Del {
			if !exists {
				return ErrNoID
			}
			del[op.ID], ins[op.ID], n = true, false, n-1
		} else if op.Part == "" {
			return ErrBadPart
		} else if exists {
			return ErrDupID
		} else {
			ins[op.ID] = true
			if n++; n > a.max {
				return ErrTooMany
			}
		}
	}
	return nil
}

// View returns the materialized view ID -> LAG (nil Lag means NULL).
func (a *API) View() map[int64]*int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return maps.Clone(a.view)
}

// SelfCheck verifies invariants 1-4 on a built-in pseudo-random op sequence.
func (a *API) SelfCheck() error {
	var ops []Op
	r := uint64(7)
	for i := 1; i <= 400; i++ { // deterministic inserts (LCG)
		r = r*6364136223846793005 + 1442695040888963407
		ops = append(ops, Op{ID: int64(i), Part: string(rune('a' + r>>33%3)), Sort: int64(r >> 43 % 50), Val: int64(r >> 53 % 10)})
	}
	for i := 2; i <= 400; i += 3 { // delete+reinsert churn appended after the inserts
		ops = append(ops, Op{Del: true, ID: int64(i)}, Op{ID: int64(i), Part: "z", Sort: int64(i % 7), Val: int64(i % 5)})
	}
	x, down := New(1<<20), map[int64]*int64{}
	for i, op := range ops {
		cs, err := x.Apply([]Op{op})
		if err != nil {
			return fmt.Errorf("selfcheck step %d: %w", i, err)
		}
		for _, c := range cs { // invariant 2: every changelog prefix is consistent
			if !lagview.ApplyChange(down, c) {
				return fmt.Errorf("selfcheck step %d: bad changelog entry %+v", i, c)
			}
		}
		if !eqView(x.view, x.lv.Batch()) || !eqView(x.view, down) { // invariant 1
			return fmt.Errorf("selfcheck: view diverged at step %d", i)
		}
	}
	a1, a2 := New(1<<20), New(1<<20) // invariant 3: insertion order must not matter
	_, _ = a1.Apply(ops[:400])
	for i := 399; i >= 0; i-- {
		_, _ = a2.Apply([]Op{ops[i]})
	}
	if !eqView(a1.View(), a2.View()) {
		return errors.New("selfcheck: insertion order changed the view")
	}
	y := New(2) // invariant 4: rejected batches leave no trace
	_, _ = y.Apply([]Op{{ID: 1, Part: "p"}, {ID: 2, Part: "p"}})
	before := y.View()
	bads := [][]Op{{{ID: 1, Part: "p"}}, {{Del: true, ID: 9}}, {{ID: 3}}, {{ID: 3, Part: "p"}}, {{ID: 4, Part: "p"}, {Del: true, ID: 9}}}
	for _, b := range bads {
		if cs, err := y.Apply(b); err == nil || cs != nil {
			return fmt.Errorf("selfcheck: bad batch %v not rejected cleanly", b)
		}
	}
	if !eqView(before, y.View()) {
		return errors.New("selfcheck: rejected batch left a trace")
	}
	return nil
}

func eqView(a, b map[int64]*int64) bool { return maps.EqualFunc(a, b, lagview.EqLag) }
