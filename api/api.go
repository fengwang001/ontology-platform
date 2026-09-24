// Package api is the outward-facing incremental materialized view.
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/agg"
	"ontology/delta"
)

// The three mutually-distinct sentinel failures required by fault injection.
var (
	ErrRetractMissing = agg.ErrRetractMissing
	ErrOverflow       = agg.ErrOverflow
	ErrTooManyGroups  = errors.New("api: active group count exceeds maxGroups")
)

// Quad is the COUNT/SUM/MIN/MAX tuple; HasMin/HasMax encode "absent", never 0.
type Quad = agg.Counter

const maxI64 = 1<<63 - 1

type View struct {
	mu     sync.RWMutex
	max    int
	groups map[string]*agg.Group
}

type step struct {
	g  *agg.Group
	ev delta.Event
}

// New builds a view capped at maxGroups concurrently active (non-empty) groups.
func New(maxGroups int) *View { return &View{max: maxGroups, groups: map[string]*agg.Group{}} }

// Feed applies every event or none: on the first rejection it replays applied
// steps in reverse with exact inverses and drops keys this batch created.
func (v *View) Feed(evs []delta.Event) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, ev := range evs {
		if !ev.Valid() {
			return delta.ErrInvalidEvent
		}
	}
	done := make([]step, 0, len(evs))
	created := map[string]bool{}
	undo := func() {
		for i := len(done) - 1; i >= 0; i-- {
			inv := done[i].ev
			if inv.Op == delta.Insert {
				inv.Op = delta.Retract
			} else {
				inv.Op = delta.Insert
			}
			_ = done[i].g.Apply(inv) // exact inverse of an applied step cannot fail
		}
		for k := range created {
			delete(v.groups, k)
		}
	}
	for _, ev := range evs {
		g, ok := v.groups[ev.Key]
		if !ok {
			active := 0
			for _, x := range v.groups {
				if x.Value().Count > 0 {
					active++
				}
			}
			if active >= v.max {
				undo()
				return ErrTooManyGroups
			}
			g = agg.NewGroup()
			v.groups[ev.Key], created[ev.Key] = g, true
		}
		if err := g.Apply(ev); err != nil {
			undo()
			return err
		}
		done = append(done, step{g, ev})
	}
	for k, g := range v.groups { // a group drained to zero is no longer active
		if g.Value().Count == 0 {
			delete(v.groups, k)
		}
	}
	return nil
}

// Snapshot returns the tuple for key (an absent/emptied key is an empty group).
func (v *View) Snapshot(key string) Quad {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if g := v.groups[key]; g != nil {
		return g.Value()
	}
	return Quad{}
}

func ge(v int64, op delta.Op) delta.Event { return delta.Event{Key: "g", Val: v, Op: op} }

func sixStepStream() ([]delta.Event, []Quad) {
	return []delta.Event{
			ge(5, delta.Insert), ge(2, delta.Insert), ge(9, delta.Insert),
			ge(9, delta.Retract), ge(2, delta.Insert), ge(2, delta.Retract),
		}, []Quad{
			{Count: 1, Sum: 5, Min: 5, Max: 5, HasMin: true, HasMax: true},
			{Count: 2, Sum: 7, Min: 2, Max: 5, HasMin: true, HasMax: true},
			{Count: 3, Sum: 16, Min: 2, Max: 9, HasMin: true, HasMax: true},
			{Count: 2, Sum: 7, Min: 2, Max: 5, HasMin: true, HasMax: true},
			{Count: 3, Sum: 9, Min: 2, Max: 5, HasMin: true, HasMax: true},
			{Count: 2, Sum: 7, Min: 2, Max: 5, HasMin: true, HasMax: true},
		}
}

// SelfCheck replays built-in streams on fresh views, verifying the invariants.
func (v *View) SelfCheck() error {
	stream, want := sixStepStream()
	w := New(8)
	for i, ev := range stream {
		if err := w.Feed([]delta.Event{ev}); err != nil {
			return fmt.Errorf("selfcheck step %d: %w", i+1, err)
		}
		if got := w.Snapshot("g"); got != want[i] {
			return fmt.Errorf("selfcheck step %d: got %+v want %+v", i+1, got, want[i])
		}
	}
	before := w.Snapshot("g")
	if err := w.Feed([]delta.Event{ge(42, delta.Insert), ge(42, delta.Retract)}); err != nil {
		return err
	}
	if w.Snapshot("g") != before {
		return errors.New("selfcheck: retract is not the inverse of insert")
	}
	if q := New(1).Snapshot("empty"); q != (Quad{}) {
		return errors.New("selfcheck: empty MIN/MAX must be absent, not 0")
	}
	cases := []struct {
		feed []delta.Event
		want error
	}{
		{[]delta.Event{ge(1, delta.Retract)}, ErrRetractMissing},
		{[]delta.Event{{Key: "a", Val: 1, Op: delta.Insert}, {Key: "b", Val: 1, Op: delta.Insert}}, ErrTooManyGroups},
		{[]delta.Event{ge(maxI64, delta.Insert), ge(1, delta.Insert)}, ErrOverflow},
	}
	for i, c := range cases {
		z := New(1)
		if err := z.Feed(c.feed); !errors.Is(err, c.want) {
			return fmt.Errorf("selfcheck fault %d: got %v want %v", i, err, c.want)
		}
		for _, ev := range c.feed {
			if z.Snapshot(ev.Key) != (Quad{}) {
				return fmt.Errorf("selfcheck fault %d left state behind", i)
			}
		}
	}
	return nil
}
