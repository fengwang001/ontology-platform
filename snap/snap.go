// Package snap builds full-base and per-key-delta checkpoints of a
// state.State and recovers state by overlaying them in checkpoint order.
package snap

import (
	"errors"
	"fmt"

	"ontology/state"
)

// ErrNoBase is returned by Recover when no checkpoint has ever been taken.
var ErrNoBase = errors.New("snap: no base checkpoint to recover from")

// Change is one entry of a delta: a new value or a tombstone deletion.
type Change struct {
	Val  int64
	Tomb bool
}

// Store keeps the base checkpoint, ordered deltas, the last-checkpoint view
// and an unexported count of keys traversed to build the latest delta.
type Store struct {
	haveBase bool
	base     map[string]int64
	last     map[string]int64
	deltas   []map[string]Change
	scanned  int // keys traversed while generating the most recent delta
}

// New returns an empty checkpoint store.
func New() *Store { return &Store{} }

// Checkpoint records a base on the first call and, afterwards, a delta that
// contains only keys whose net effect since the previous checkpoint differs
// from that checkpoint's view. It is the only place scanned is updated.
func (s *Store) Checkpoint(st *state.State) {
	s.scanned = 0
	if !s.haveBase {
		s.base = st.Clone()
		s.last = st.Clone()
		s.deltas = nil
		s.haveBase = true
		st.ResetDirty()
		return
	}
	delta := map[string]Change{}
	for k := range st.Dirty() { // iterate dirty keys only, never the whole map
		s.scanned++
		cur, live := st.Get(k)
		old, had := s.last[k]
		switch {
		case live && (!had || old != cur):
			delta[k] = Change{Val: cur} // set / value change
			s.last[k] = cur
		case !live && had:
			delta[k] = Change{Tomb: true} // deleted since last checkpoint
			delete(s.last, k)
			// !live && !had: created and deleted again; net zero, omitted.
			// live && had && old==cur: changed back; net zero, omitted.
		}
	}
	s.deltas = append(s.deltas, delta)
	st.ResetDirty()
}

// Recover overlays the base and all deltas in checkpoint order: later writes
// win and tombstones delete. It never mutates the stored checkpoints.
func (s *Store) Recover() (map[string]int64, error) {
	if !s.haveBase {
		return nil, ErrNoBase
	}
	out := make(map[string]int64, len(s.base))
	for k, v := range s.base {
		out[k] = v
	}
	for _, d := range s.deltas {
		for k, c := range d {
			if c.Tomb {
				delete(out, k)
			} else {
				out[k] = c.Val
			}
		}
	}
	return out, nil
}

// section3 replays the mandated maxKeys=8 scenario on fresh objects and
// returns every checkpoint's exact contents for in-package verification.
func section3() (map[string]int64, []map[string]Change, map[string]int64) {
	st, s := state.New(), New()
	set, del := func(k string, v int64) { st.Set(k, v) }, func(k string) { st.Delete(k) }
	set("a", 1)
	set("b", 2)
	set("c", 3)
	s.Checkpoint(st)
	set("a", 10)
	del("b")
	set("d", 4)
	s.Checkpoint(st)
	set("a", 1)
	set("c", 30)
	set("e", 5)
	s.Checkpoint(st)
	set("c", 3)
	s.Checkpoint(st)
	got, _ := s.Recover()
	return s.base, s.deltas, got
}

// SelfCheck runs the built-in white-box scenarios and returns verdicts only
// (never the raw traversal counter): exact section-3 contents and recovery,
// and dirty-bounded traversal over several m tiers.
func SelfCheck() (contentOK, boundOK bool) {
	base, deltas, got := section3()
	want := map[string]int64{"a": 1, "c": 3, "d": 4, "e": 5}
	contentOK = fmt.Sprint(base) == fmt.Sprint(map[string]int64{"a": 1, "b": 2, "c": 3}) &&
		len(deltas) == 3 &&
		fmt.Sprint(deltas[0]) == fmt.Sprint(map[string]Change{"a": {Val: 10}, "b": {Tomb: true}, "d": {Val: 4}}) &&
		fmt.Sprint(deltas[1]) == fmt.Sprint(map[string]Change{"a": {Val: 1}, "c": {Val: 30}, "e": {Val: 5}}) &&
		fmt.Sprint(deltas[2]) == fmt.Sprint(map[string]Change{"c": {Val: 3}}) &&
		fmt.Sprint(got) == fmt.Sprint(want)
	boundOK = true
	for _, m := range []int{100, 1000, 10000} {
		st, s := state.New(), New()
		for i := 0; i < m; i++ {
			st.Set(fmt.Sprintf("k%05d", i), int64(i))
		}
		s.Checkpoint(st) // base; dirty cleared
		st.Set("k00000", -1)
		s.Checkpoint(st) // one dirty key, regardless of m
		if s.scanned != 1 {
			boundOK = false
		}
	}
	return contentOK, boundOK
}
