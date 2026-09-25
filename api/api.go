// Package api is the public entry point for change-stream offset archival
// and retention across partitions. State is in-process memory only.
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/off"
)

// ErrInvalidRetention is returned by New for a non-positive retention.
var ErrInvalidRetention = errors.New("api: retention must be positive")

// V is the in-memory archival service. The zero value is not ready; use New.
type V struct {
	mu        sync.RWMutex
	retention int64
	parts     map[int]*off.State
}

// New builds a service; non-positive retention returns ErrInvalidRetention.
func New(retention int64) (V, error) {
	if retention <= 0 {
		return V{}, ErrInvalidRetention
	}
	return V{retention: retention, parts: map[int]*off.State{}}, nil
}

// part returns the partition's state, creating it on first use.
func (v *V) part(p int) *off.State {
	s, ok := v.parts[p]
	if !ok {
		s = off.New()
		v.parts[p] = s
	}
	return s
}
func (v *V) Commit(p int, offVal, ts int64) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.part(p).Commit(offVal, ts)
}
func (v *V) Checkpoint(p int) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	s, ok := v.parts[p]
	if !ok {
		return off.ErrNoCommit // never create an empty partition on failure
	}
	return s.Checkpoint()
}
func (v *V) Evict(p int, now int64) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.part(p).Evict(now, v.retention)
}
func (v *V) Committed(p int) (int64, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	s, ok := v.parts[p]
	if !ok {
		return 0, false
	}
	return s.Committed()
}

// Restart returns every partition's recovery position =
// max(checkpoint, largest surviving archive offset).
func (v *V) Restart() map[int]int64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := make(map[int]int64, len(v.parts))
	for p, s := range v.parts {
		out[p] = s.Recover()
	}
	return out
}

// SelfCheck replays the built-in eight-step sequence and verifies all four
// invariants; want[] are the per-step batch results derived in NOTES.md.
func (v *V) SelfCheck() error {
	if _, e := New(0); !errors.Is(e, ErrInvalidRetention) {
		return errors.New("selfcheck: non-positive retention accepted")
	}
	z, _ := New(10)
	want := []int64{100, 100, 110, 120, 120, 130, 130, 100}
	mono := []bool{true, true, true, true, true, true, true, false}
	prev := off.NoCheckpoint
	for i := 0; i < len(want); i++ {
		var e error
		switch i {
		case 0:
			e = z.Commit(0, 100, 0)
		case 1:
			e = z.Checkpoint(0)
		case 2:
			e = z.Commit(0, 110, 10)
		case 3:
			e = z.Commit(0, 120, 15)
		case 4:
			e = z.Evict(0, 20)
		case 5:
			e = z.Commit(0, 130, 30)
		case 6:
			e = z.Evict(0, 40)
		case 7:
			e = z.Evict(0, 45)
		}
		if e != nil {
			return fmt.Errorf("selfcheck step %d: %w", i+1, e)
		}
		got := z.Restart()[0]
		if got != want[i] { // invariants 1 and 2 (step 8: cp survives empty archive)
			return fmt.Errorf("selfcheck step %d: recover %d != batch %d", i+1, got, want[i])
		}
		if mono[i] && i > 0 && got < prev { // invariant 3
			return fmt.Errorf("selfcheck step %d: recovery regressed %d->%d", i+1, prev, got)
		}
		prev = got
	}
	// Invariant 4: four distinct rejections leave no state change.
	if z.Commit(7, 5, 0) != nil || z.Evict(7, 20) != nil {
		return errors.New("selfcheck: seed rejected")
	}
	for _, c := range []struct {
		want error
		run  func() error
	}{
		{off.ErrCommitNotMonotonic, func() error { return z.Commit(7, 5, 1) }},
		{off.ErrCommitNotMonotonic, func() error { return z.Commit(7, 6, -1) }},
		{off.ErrNowRewound, func() error { return z.Evict(7, 19) }},
		{off.ErrNoCommit, func() error { return z.Checkpoint(9) }},
	} {
		if e := c.run(); !errors.Is(e, c.want) {
			return fmt.Errorf("selfcheck: want %v got %v", c.want, e)
		}
	}
	r := z.Restart()
	if c, ok := z.Committed(7); !ok || c != 5 {
		return errors.New("selfcheck: committed changed after rejection")
	}
	if _, p := r[9]; p || r[0] != 100 {
		return errors.New("selfcheck: rejection changed recovery state")
	}
	return nil
}
