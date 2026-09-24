// Package api is the public entry point to the incremental EXCEPT ALL view.
package api

import (
	"errors"
	"sync"

	"ontology/exc"
)

var (
	ErrInvalidChange = errors.New("api: invalid change (empty row, zero delta, or bad side)")
	ErrUnderflow     = exc.ErrUnderflow
	ErrTooManyRows   = exc.ErrTooManyRows
)

// Side is L or R; the zero value and any other value are invalid.
type Side = exc.Side

const (
	L = exc.Left
	R = exc.Right
)

type Change struct {
	Side  Side
	Row   string
	Delta int
}

type Out struct {
	Row   string
	Delta int
}

// View is the thread-safe materialized L EXCEPT ALL R view.
type View struct {
	mu sync.RWMutex
	op *exc.Operator
}

func New(maxRows int) *View { return &View{op: exc.New(maxRows)} }

// Apply validates the whole batch then applies it in order; each accepted
// entry yields at most one Out. Any rejection cancels the entire batch.
func (v *View) Apply(chs []Change) ([]Out, error) {
	in := make([]exc.Change, len(chs))
	for i, c := range chs {
		if c.Row == "" || c.Delta == 0 || (c.Side != L && c.Side != R) {
			return nil, ErrInvalidChange
		}
		in[i] = exc.Change{Row: c.Row, Side: c.Side, Delta: c.Delta}
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	res, err := v.op.Apply(in)
	if err != nil {
		return nil, err
	}
	outs := make([]Out, len(res))
	for i, o := range res {
		outs[i] = Out{Row: o.Row, Delta: o.Delta}
	}
	return outs, nil
}

// View returns an independent copy of the current materialized view.
func (v *View) View() map[string]int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.op.View()
}

// SelfCheck replays a built-in sequence on a scratch view and verifies the
// four invariants. It never mutates the receiver.
func (v *View) SelfCheck() error {
	w := New(100)
	lc, rc := map[string]int{}, map[string]int{}
	ed := []struct {
		s Side
		x string
		d int
	}{{R, "a", 1}, {L, "a", 1}, {L, "a", 1}, {L, "a", 2}, {R, "a", 1},
		{R, "a", 3}, {L, "a", -1}, {R, "a", -4}, {L, "b", 1}, {R, "b", 1}}
	eq := func(p, q map[string]int) bool {
		if len(p) != len(q) {
			return false
		}
		for k, n := range p {
			if q[k] != n {
				return false
			}
		}
		return true
	}
	recalc := func() map[string]int {
		m := map[string]int{}
		for x, n := range lc {
			if d := n - rc[x]; d > 0 {
				m[x] = d
			}
		}
		return m // rows only on R contribute 0 and stay absent
	}
	for _, e := range ed {
		outs, err := w.Apply([]Change{{e.s, e.x, e.d}})
		if err != nil {
			return err
		}
		if len(outs) > 1 || (len(outs) == 1 && (outs[0].Delta == 0 || absInt(outs[0].Delta) > absInt(e.d))) {
			return errors.New("selfcheck: change-minimality violated")
		}
		if e.s == L {
			lc[e.x] += e.d
		} else {
			rc[e.x] += e.d
		}
		got := w.View()
		if !eq(got, recalc()) {
			return errors.New("selfcheck: prefix differs from batch recompute")
		}
		for _, n := range got {
			if n <= 0 {
				return errors.New("selfcheck: non-positive multiplicity in view")
			}
		}
	}
	snap := w.View()
	bad := [][]Change{{{L, "", 1}}, {{L, "a", 0}}, {{Side(9), "a", 1}}, {{L, "a", -99}}}
	for _, b := range bad {
		if _, err := w.Apply(b); err == nil || !eq(w.View(), snap) {
			return errors.New("selfcheck: rejection missing or left a trace")
		}
	}
	small := New(1)
	if _, err := small.Apply([]Change{{L, "a", 1}, {L, "b", 1}}); !errors.Is(err, ErrTooManyRows) {
		return errors.New("selfcheck: expected ErrTooManyRows")
	}
	if len(small.View()) != 0 {
		return errors.New("selfcheck: rejected batch was not atomic")
	}
	return nil
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
