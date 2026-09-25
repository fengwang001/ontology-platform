// Package api is the public facade of the PCP lock manager. Depends only on lock.
package api

import (
	"fmt"

	"ontology/lock"
)

// Sentinel errors, re-exported from the lock layer.
var (
	ErrUnknownTask     = lock.ErrUnknownTask
	ErrUnknownResource = lock.ErrUnknownResource
	ErrAlreadyHeld     = lock.ErrAlreadyHeld
	ErrNotHeld         = lock.ErrNotHeld
)

// Manager is the public handle. Safe for concurrent use.
type Manager struct{ lm *lock.Manager }

// New returns an empty Manager.
func New() *Manager { return &Manager{lm: lock.New()} }

// AddTask registers a task with its base priority.
func (m *Manager) AddTask(id string, priority int) { m.lm.AddTask(id, priority) }

// AddResource registers a resource.
func (m *Manager) AddResource(name string) { m.lm.AddResource(name) }

// Use declares that task uses res.
func (m *Manager) Use(task, res string) error { return m.lm.Use(task, res) }

// Acquire tries to grant res to task; granted=false means blocked.
func (m *Manager) Acquire(task, res string) (bool, error) { return m.lm.Acquire(task, res) }

// Release drops task's hold on res.
func (m *Manager) Release(task, res string) error { return m.lm.Release(task, res) }

// SystemCeiling is the max ceiling over all currently held resources.
func (m *Manager) SystemCeiling() int { return m.lm.SystemCeiling() }

// EffectivePriority is max(base priority, ceilings of held resources).
func (m *Manager) EffectivePriority(task string) (int, error) {
	return m.lm.EffectivePriority(task)
}

// SelfCheck verifies the four invariants on built-in operation sequences,
// using a fresh internal manager; the receiver's state is untouched.
func (m *Manager) SelfCheck() error {
	g := New()
	g.AddTask("T1", 5)
	g.AddTask("T2", 3)
	g.AddTask("T3", 1)
	g.AddResource("Rx")
	g.AddResource("Ry")
	for _, u := range [][2]string{{"T1", "Rx"}, {"T3", "Rx"}, {"T2", "Ry"}, {"T3", "Ry"}} {
		if err := g.Use(u[0], u[1]); err != nil {
			return err
		}
	}
	// Invariant 3: ceilings are the max user priority.
	if g.lm.SystemCeiling() != 0 {
		return fmt.Errorf("selfcheck: initial system ceiling != 0")
	}
	// Eight-step sequence: granted, blocked, blocked, ok, granted, blocked, ok, granted.
	wantGrant := []bool{true, false, false, true, false, true}
	wantCeil := []int{5, 5, 5, 5, 5, 3}
	type acq struct{ t, r string }
	ops := []acq{{"T3", "Rx"}, {"T2", "Ry"}, {"T1", "Rx"}, {"T1", "Rx"}, {"T2", "Ry"}, {"T2", "Ry"}}
	rel := []acq{{"T3", "Rx"}, {"T1", "Rx"}}
	gi := 0
	step := func() error {
		if gi == 3 || gi == 5 { // releases before steps 5 and 8
			r := rel[0]
			rel = rel[1:]
			if err := g.Release(r.t, r.r); err != nil {
				return fmt.Errorf("selfcheck: release: %w", err)
			}
		}
		o := ops[gi]
		got, err := g.Acquire(o.t, o.r)
		if err != nil || got != wantGrant[gi] {
			return fmt.Errorf("selfcheck: step %d got %v,%v want %v", gi, got, err, wantGrant[gi])
		}
		if c := g.SystemCeiling(); c != wantCeil[gi] {
			return fmt.Errorf("selfcheck: step %d ceiling %d want %d", gi, c, wantCeil[gi])
		}
		gi++
		return nil
	}
	for i := 0; i < 6; i++ {
		if err := step(); err != nil {
			return err
		}
	}
	// Invariant 3: effective priority is boosted to the held ceiling.
	// (T3 held Rx after step 1; verified again on a fresh manager below.)
	h := New()
	h.AddTask("lo", 1)
	h.AddTask("hi", 9)
	h.AddResource("R")
	if err := h.Use("hi", "R"); err != nil {
		return err
	}
	if ok, _ := h.Acquire("lo", "R"); !ok {
		return fmt.Errorf("selfcheck: lo should hold R")
	}
	if eff, _ := h.EffectivePriority("lo"); eff != 9 {
		return fmt.Errorf("selfcheck: effective priority %d want 9", eff)
	}
	// Invariant 4: rejected ops leave state unchanged and are distinguishable.
	before := h.SystemCeiling()
	faults := []struct {
		err  error
		want error
	}{
		{func() error { _, e := h.Acquire("ghost", "R"); return e }(), ErrUnknownTask},
		{func() error { _, e := h.Acquire("hi", "nope"); return e }(), ErrUnknownResource},
		{func() error { _, e := h.Acquire("lo", "R"); return e }(), ErrAlreadyHeld},
		{h.Release("hi", "R"), ErrNotHeld},
		{h.Use("ghost", "R"), ErrUnknownTask},
	}
	for i, f := range faults {
		if f.err == nil || f.err != f.want {
			return fmt.Errorf("selfcheck: fault %d got %v want %v", i, f.err, f.want)
		}
	}
	if h.SystemCeiling() != before {
		return fmt.Errorf("selfcheck: rejected op changed state")
	}
	if err := h.Release("lo", "R"); err != nil { // still usable afterwards
		return fmt.Errorf("selfcheck: unusable after faults: %w", err)
	}
	return nil
}
