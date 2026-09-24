// Package api is the external entry point of the in-memory materialized view.
package api

import (
	"errors"
	"fmt"
	"math"
	"sync"

	"ontology/agg"
	"ontology/delta"
)

var ErrTooManyGroups = errors.New("api: active group count would exceed maxGroups")

// Quad is one group's four aggregates; HasMin/HasMax false means "absent", never 0.
type Quad struct {
	Count          int
	Sum            int64
	Min, Max       int64
	HasMin, HasMax bool
}

type View struct {
	mu     sync.RWMutex
	max    int
	groups map[string]*agg.Group
}

func New(maxGroups int) *View { return &View{max: maxGroups, groups: map[string]*agg.Group{}} }

// Feed applies evs atomically: a rejection reverses events already applied in
// this call, so no partial state survives; groups drained to zero disappear.
func (v *View) Feed(evs []delta.Event) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	for i, ev := range evs {
		if !ev.Valid() {
			v.rollback(evs[:i])
			return delta.ErrInvalidOp
		}
		g := v.groups[ev.Key]
		if g == nil {
			if len(v.groups) >= v.max {
				v.rollback(evs[:i])
				return ErrTooManyGroups
			}
			g = agg.New()
			v.groups[ev.Key] = g
		}
		if err := g.Apply(ev); err != nil {
			v.rollback(evs[:i])
			return err
		}
	}
	v.purge()
	return nil
}

func (v *View) rollback(applied []delta.Event) { // reverse prefix; the inverse ops are always legal
	for i := len(applied) - 1; i >= 0; i-- {
		ev := applied[i]
		op := delta.Insert + delta.Retract - ev.Op // Insert<->Retract
		_ = v.groups[ev.Key].Apply(delta.Event{Key: ev.Key, Val: ev.Val, Op: op})
	}
	v.purge()
}

func (v *View) purge() {
	for k, g := range v.groups {
		if g.Count() == 0 {
			delete(v.groups, k)
		}
	}
}

// Snapshot returns key's quad (zero Quad when unknown); safe for concurrent callers.
func (v *View) Snapshot(key string) Quad {
	v.mu.RLock()
	defer v.mu.RUnlock()
	g := v.groups[key]
	if g == nil {
		return Quad{}
	}
	mn, hm := g.Min()
	mx, hx := g.Max()
	return Quad{Count: g.Count(), Sum: g.Sum(), Min: mn, Max: mx, HasMin: hm, HasMax: hx}
}

// SelfCheck replays built-in streams and verifies all four invariants, the
// three distinct sentinel errors and atomic batch rollback.
func (v *View) SelfCheck() error {
	I := func(x int64) delta.Event { return delta.Event{Key: "g", Val: x, Op: delta.Insert} }
	R := func(x int64) delta.Event { return delta.Event{Key: "g", Val: x, Op: delta.Retract} }
	Q := func(n int, s, lo, hi int64) Quad { return Quad{n, s, lo, hi, true, true} }
	// Invariant 1 & 3, the required six-step table (duplicate 2 in steps 5/6).
	six := []delta.Event{I(5), I(2), I(9), R(9), I(2), R(2)}
	want := []Quad{Q(1, 5, 5, 5), Q(2, 7, 2, 5), Q(3, 16, 2, 9), Q(2, 7, 2, 5), Q(3, 9, 2, 5), Q(2, 7, 2, 5)}
	w := New(8)
	for i, ev := range six {
		if err := w.Feed([]delta.Event{ev}); err != nil || w.Snapshot("g") != want[i] {
			return fmt.Errorf("six-step %d: %+v want %+v (%v)", i, w.Snapshot("g"), want[i], err)
		}
	}
	neg := []delta.Event{I(-5), I(-5), I(3), R(-5), I(-10), R(3)}
	nwant := []Quad{Q(1, -5, -5, -5), Q(2, -10, -5, -5), Q(3, -7, -5, 3), Q(2, -2, -5, 3), Q(3, -12, -10, 3), Q(2, -15, -10, -5)}
	nw := New(4)
	for i, ev := range neg {
		if err := nw.Feed([]delta.Event{ev}); err != nil || nw.Snapshot("g") != nwant[i] {
			return fmt.Errorf("neg-step %d: %+v want %+v (%v)", i, nw.Snapshot("g"), nwant[i], err)
		}
	}
	// Invariant 2: Insert then Retract returns to the pre-insert state.
	before := w.Snapshot("g")
	if err := w.Feed([]delta.Event{I(42)}); err != nil {
		return err
	}
	if err := w.Feed([]delta.Event{R(42)}); err != nil || w.Snapshot("g") != before {
		return errors.New("retract is not the inverse of insert")
	}
	// Invariant 3: draining the live set {5,2} reports absent, not zero.
	if err := w.Feed([]delta.Event{R(5), R(2)}); err != nil || w.Snapshot("g") != (Quad{}) {
		return errors.New("drained group is not empty/absent")
	}
	// Invariant 4: three distinct errors, each leaving no trace.
	if agg.ErrRetractUnknown == agg.ErrSumOverflow || agg.ErrSumOverflow == ErrTooManyGroups || agg.ErrRetractUnknown == ErrTooManyGroups {
		return errors.New("sentinel errors are not distinct")
	}
	t := New(4)
	if err := t.Feed([]delta.Event{I(1)}); err != nil {
		return err
	}
	pre := t.Snapshot("g")
	rej := func(evs []delta.Event, want error) bool {
		return errors.Is(t.Feed(evs), want) && t.Snapshot("g") == pre
	}
	if !rej([]delta.Event{R(9)}, agg.ErrRetractUnknown) ||
		!rej([]delta.Event{I(2), R(99)}, agg.ErrRetractUnknown) || // whole batch rolls back
		!rej([]delta.Event{I(2), I(math.MaxInt64)}, agg.ErrSumOverflow) {
		return errors.New("a rejection was mistyped or left a trace")
	}
	tm := New(1)
	if err := tm.Feed([]delta.Event{I(1)}); err != nil {
		return err
	}
	if err := tm.Feed([]delta.Event{{Key: "y", Val: 1, Op: delta.Insert}}); !errors.Is(err, ErrTooManyGroups) || tm.Snapshot("y") != (Quad{}) {
		return errors.New("group-cap rejection leaked")
	}
	return t.Feed([]delta.Event{I(3)}) // view still usable after rejections
}
