// Package api is the external incremental materialized view: New/Feed/Snapshot/SelfCheck.
package api

import (
	"errors"
	"math"
	"math/rand"
	"sync"

	"ontology/agg"
	"ontology/delta"
)

var ErrTooManyGroups = errors.New("api: active group count exceeds limit")

// Snap is one group's tuple; Present is false when the key has no live values.
type Snap struct {
	Count, Sum, Min, Max  int64
	MinOK, MaxOK, Present bool
}

type View struct {
	mu     sync.RWMutex
	groups map[string]*agg.Group
	max    int
}

func New(n int) *View { return &View{groups: map[string]*agg.Group{}, max: n} }

func (v *View) applyOneLocked(ev delta.Event) error {
	g, exists := v.groups[ev.Key]
	if !exists {
		if ev.Op == delta.Retract {
			return delta.ErrRetractMissing
		}
		if len(v.groups) >= v.max {
			return ErrTooManyGroups
		}
		g = agg.NewGroup()
		v.groups[ev.Key] = g // registered now; deleted below if Apply rejects
	}
	if err := g.Apply(ev); err != nil {
		if !exists {
			delete(v.groups, ev.Key)
		}
		return err
	}
	if ev.Op == delta.Retract {
		if c, _, _, _, _, _ := g.Value(); c == 0 {
			delete(v.groups, ev.Key) // prune emptied group
		}
	}
	return nil
}

func (v *View) rollback(done []delta.Event) {
	for j := len(done) - 1; j >= 0; j-- { // reverse; each inverse is legal
		inv := done[j]
		inv.Op ^= 1 // Insert<->Retract are adjacent iota values
		_ = v.applyOneLocked(inv)
	}
}

// Feed applies a batch atomically; a rejecting event rolls back earlier changes.
func (v *View) Feed(evs []delta.Event) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	for i, ev := range evs {
		err := delta.ErrInvalidEvent
		if ev.Valid() {
			err = v.applyOneLocked(ev)
		}
		if err != nil {
			v.rollback(evs[:i])
			return err
		}
	}
	return nil
}

func (v *View) Snapshot(key string) (Snap, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	g, ok := v.groups[key]
	if !ok {
		return Snap{}, false
	}
	c, s, mn, mx, mok, xok := g.Value()
	return Snap{c, s, mn, mx, mok, xok, true}, true
}

func refTotals(a map[int64]int64) (c, s, mn, mx int64, mok, xok bool) {
	for v, n := range a {
		c, s = c+n, s+v*n
		if mok {
			mn, mx = min(mn, v), max(mx, v)
		} else {
			mn, mx, mok, xok = v, v, true, true
		}
	}
	return
}

// SelfCheck drives built-in streams against full recomputation plus the three faults; nil = pass.
func (v *View) SelfCheck() error {
	w, rng := New(16), rand.New(rand.NewSource(7))
	live := map[string]map[int64]int64{}
	for _, ch := range "abcde" {
		live[string(ch)] = map[int64]int64{}
	}
	for i := 0; i < 1000; i++ {
		k := string(rune('a' + rng.Intn(5)))
		val, op := int64(rng.Intn(19))-9, delta.Insert
		if m := live[k]; len(m) > 0 && rng.Intn(2) == 0 {
			op = delta.Retract
			for val = range m {
				break
			}
		}
		if err := w.Feed([]delta.Event{{Key: k, Val: val, Op: op}}); err != nil {
			return err
		}
		m := live[k]
		if op == delta.Insert {
			m[val]++
		} else if m[val]--; m[val] == 0 {
			delete(m, val)
		}
		got, present := w.Snapshot(k)
		c, s, mn, mx, mok, xok := refTotals(m)
		if present != (c != 0) || present && got != (Snap{c, s, mn, mx, mok, xok, true}) {
			return errors.New("selfcheck: diverged from recomputation")
		}
	}
	for _, p := range [][2]any{
		{[]delta.Event{{Key: "g", Val: 1, Op: delta.Retract}}, delta.ErrRetractMissing},
		{[]delta.Event{{Key: "a", Val: 1, Op: delta.Insert}, {Key: "b", Val: 1, Op: delta.Insert}}, ErrTooManyGroups},
		{[]delta.Event{{Key: "g", Val: math.MaxInt64, Op: delta.Insert}, {Key: "g", Val: 1, Op: delta.Insert}}, agg.ErrSumOverflow},
	} {
		q := New(1)
		before, _ := q.Snapshot("g")
		if err := q.Feed(p[0].([]delta.Event)); !errors.Is(err, p[1].(error)) {
			return errors.New("selfcheck: wrong or absent sentinel")
		}
		if after, _ := q.Snapshot("g"); after != before {
			return errors.New("selfcheck: rejected batch left a trace")
		}
	}
	return nil
}
