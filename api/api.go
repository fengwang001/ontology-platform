// Package api exposes the lock-protected incremental aggregate view.
package api

import (
	"errors"
	"fmt"
	"math"
	"sync"

	"ontology/agg"
	"ontology/delta"
)

// ErrTooManyGroups is rejected before a new group is admitted.
var ErrTooManyGroups = errors.New("api: active group count exceeds maxGroups")

// Four is one group's current aggregates; HasMin/HasMax express absence.
type Four struct {
	Count, Sum, Min, Max int64
	HasMin, HasMax       bool
}

// View keeps every group's aggregates in process memory.
type View struct {
	mu     sync.RWMutex
	max    int
	groups map[string]*agg.Group
}

// New creates a view admitting at most maxGroups active groups.
func New(maxGroups int) *View {
	return &View{max: maxGroups, groups: map[string]*agg.Group{}}
}

type applied struct {
	key     string
	g       *agg.Group
	ev      delta.Event
	created bool
}

// Feed applies the whole batch or none of it: any rejection rolls back every
// earlier event with its exact inverse, and drops groups created by the batch.
func (v *View) Feed(evs []delta.Event) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	done := []applied{}
	undo := func() {
		for i := len(done) - 1; i >= 0; i-- {
			c := done[i]
			inv := c.ev
			if inv.Op == delta.Insert {
				inv.Op = delta.Retract
			} else {
				inv.Op = delta.Insert
			}
			_ = c.g.Apply(inv)
			if c.created {
				delete(v.groups, c.key)
			}
		}
	}
	for _, ev := range evs {
		g, created := v.groups[ev.Key], false
		if g == nil {
			if len(v.groups) >= v.max {
				undo()
				return ErrTooManyGroups
			}
			g = &agg.Group{}
			v.groups[ev.Key] = g
			created = true
		}
		if err := g.Apply(ev); err != nil {
			if created {
				delete(v.groups, ev.Key)
			}
			undo()
			return err
		}
		done = append(done, applied{key: ev.Key, g: g, ev: ev, created: created})
	}
	// A fully applied batch may have emptied a group; empty groups are not
	// active, report the same Four{} as unknown keys, and must not hold slots.
	for _, c := range done {
		if g := v.groups[c.key]; g != nil && g.Empty() {
			delete(v.groups, c.key)
		}
	}
	return nil
}

// Snapshot returns the four aggregates; an unknown or emptied group is Four{}.
// It takes a read lock and is safe for concurrent goroutines.
func (v *View) Snapshot(key string) Four {
	v.mu.RLock()
	defer v.mu.RUnlock()
	g := v.groups[key]
	if g == nil {
		return Four{}
	}
	c, s, lo, hi, hn, hx := g.Value()
	return Four{Count: c, Sum: s, Min: lo, Max: hi, HasMin: hn, HasMax: hx}
}

// SelfCheck replays built-in streams on scratch views and verifies the four
// invariants: stepwise agreement, insert/retract inverse, empty-group
// semantics, and atomic rejection with three distinct sentinel errors.
func SelfCheck() error {
	v := New(8)
	steps := []delta.Event{
		{Key: "g", Val: 5, Op: delta.Insert}, {Key: "g", Val: 2, Op: delta.Insert},
		{Key: "g", Val: 9, Op: delta.Insert}, {Key: "g", Val: 9, Op: delta.Retract},
		{Key: "g", Val: 2, Op: delta.Insert}, {Key: "g", Val: 2, Op: delta.Retract},
	}
	want := []Four{{1, 5, 5, 5, true, true}, {2, 7, 2, 5, true, true},
		{3, 16, 2, 9, true, true}, {2, 7, 2, 5, true, true},
		{3, 9, 2, 5, true, true}, {2, 7, 2, 5, true, true}}
	for i, ev := range steps {
		if err := v.Feed([]delta.Event{ev}); err != nil {
			return err
		}
		if got := v.Snapshot("g"); got != want[i] {
			return fmt.Errorf("selfcheck step %d: got %+v want %+v", i, got, want[i])
		}
	}
	if err := v.Feed([]delta.Event{{Key: "z", Val: 8, Op: delta.Insert}}); err != nil {
		return err
	}
	if err := v.Feed([]delta.Event{{Key: "z", Val: 8, Op: delta.Retract}}); err != nil {
		return err
	}
	if v.Snapshot("z") != (Four{}) || v.Snapshot("absent") != (Four{}) {
		return errors.New("selfcheck: empty group not reported as absent")
	}
	before := v.Snapshot("g")
	cases := []struct {
		max   int
		init  []delta.Event
		evs   []delta.Event
		probe []delta.Event
		want  error
	}{
		{8, []delta.Event{{Key: "g", Val: 5, Op: delta.Insert}},
			[]delta.Event{{Key: "g", Val: 1, Op: delta.Insert},
				{Key: "g", Val: 100, Op: delta.Retract}},
			[]delta.Event{{Key: "g", Val: 5, Op: delta.Retract},
				{Key: "g", Val: 5, Op: delta.Insert}}, agg.ErrRetractMissing},
		{8, []delta.Event{{Key: "g", Val: 5, Op: delta.Insert}},
			[]delta.Event{{Key: "g", Val: 1, Op: 0}},
			[]delta.Event{{Key: "g", Val: 5, Op: delta.Retract},
				{Key: "g", Val: 5, Op: delta.Insert}}, delta.ErrBadOp},
		{1, []delta.Event{{Key: "g", Val: 5, Op: delta.Insert}},
			[]delta.Event{{Key: "a", Val: 1, Op: delta.Insert},
				{Key: "b", Val: 1, Op: delta.Insert}},
			[]delta.Event{{Key: "g", Val: 5, Op: delta.Retract},
				{Key: "g", Val: 5, Op: delta.Insert}}, ErrTooManyGroups},
		{8, []delta.Event{{Key: "g", Val: math.MaxInt64, Op: delta.Insert}},
			[]delta.Event{{Key: "g", Val: 1, Op: delta.Insert}},
			[]delta.Event{{Key: "g", Val: math.MaxInt64, Op: delta.Retract},
				{Key: "g", Val: math.MaxInt64, Op: delta.Insert}}, agg.ErrSumOverflow},
	}
	for i, tc := range cases {
		w := New(tc.max)
		if err := w.Feed(tc.init); err != nil {
			return err
		}
		base := w.Snapshot("g")
		if err := w.Feed(tc.evs); !errors.Is(err, tc.want) {
			return fmt.Errorf("selfcheck case %d: got %v want %v", i, err, tc.want)
		}
		if got := w.Snapshot("g"); got != base {
			return fmt.Errorf("selfcheck case %d: rejected feed left a trace", i)
		}
		if err := w.Feed(tc.probe); err != nil || w.Snapshot("g") != base {
			return fmt.Errorf("selfcheck case %d: view unusable after rejection", i)
		}
	}
	if errors.Is(agg.ErrRetractMissing, ErrTooManyGroups) ||
		errors.Is(agg.ErrRetractMissing, agg.ErrSumOverflow) ||
		errors.Is(ErrTooManyGroups, agg.ErrSumOverflow) {
		return errors.New("selfcheck: sentinel errors are not distinct")
	}
	if err := v.Feed([]delta.Event{{Key: "g", Val: 100, Op: delta.Retract}}); !errors.Is(err, agg.ErrRetractMissing) {
		return fmt.Errorf("selfcheck: got %v want retract-missing", err)
	}
	if v.Snapshot("g") != before {
		return errors.New("selfcheck: rejected retraction changed state")
	}
	return nil
}
