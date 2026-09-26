// Package api is the public face of the priority-inheritance mutex.
// It depends only on package lock.
package api

import (
	"fmt"
	"math/rand"
	"sync"

	"ontology/lock"
)

// Mutex is a priority-inheritance mutex safe for concurrent use.
type Mutex struct {
	mu sync.Mutex
	l  *lock.Lock
}

// New returns an idle mutex.
func New() *Mutex { return &Mutex{l: lock.New()} }

// Acquire takes the lock or queues the task with static priority prio.
func (m *Mutex) Acquire(id, prio int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.l.Acquire(id, prio)
}

// Release releases the lock held by id.
func (m *Mutex) Release(id int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.l.Release(id)
}

// Holder reports the current holder.
func (m *Mutex) Holder() (int, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.l.Holder()
}

// Effective reports the holder's effective priority (0 when idle).
func (m *Mutex) Effective() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.l.Effective()
}

// model is the naive O(n) reference used by SelfCheck.
type model struct {
	has    bool
	holder int
	hprio  int
	wait   map[int]int
}

func (mo *model) acquire(id, prio int) {
	if !mo.has {
		mo.has, mo.holder, mo.hprio = true, id, prio
		return
	}
	mo.wait[id] = prio
}

func (mo *model) release() {
	best, bp := 0, -1
	for id, p := range mo.wait {
		if p > bp {
			best, bp = id, p
		}
	}
	if bp < 0 {
		mo.has = false
		return
	}
	delete(mo.wait, best)
	mo.holder, mo.hprio = best, bp
}

func (mo *model) effective() int {
	if !mo.has {
		return 0
	}
	eff := mo.hprio
	for _, p := range mo.wait {
		if p > eff {
			eff = p
		}
	}
	return eff
}

// SelfCheck verifies the four invariants on a fresh lock against the
// naive reference model. It touches no shared state.
func (m *Mutex) SelfCheck() error {
	l := lock.New()
	mo := &model{wait: map[int]int{}}
	rng := rand.New(rand.NewSource(723))
	perm := rng.Perm(4000) // unique static priorities: no tie ambiguity
	next := 1
	for op := 0; op < 4000; op++ {
		if rng.Intn(2) == 0 { // acquire a fresh id
			p := perm[next-1] + 1
			if err := l.Acquire(next, p); err != nil {
				return fmt.Errorf("selfcheck acquire: %w", err)
			}
			mo.acquire(next, p)
			next++
		} else if mo.has { // release the holder
			if err := l.Release(mo.holder); err != nil {
				return fmt.Errorf("selfcheck release: %w", err)
			}
			mo.release()
		}
		h, has := l.Holder()
		e := l.Effective()
		if has != mo.has || (has && h != mo.holder) {
			return fmt.Errorf("invariant1/3: holder (%d,%v) != model (%d,%v)", h, has, mo.holder, mo.has)
		}
		if e != mo.effective() {
			return fmt.Errorf("invariant1/2: effective %d != naive %d", e, mo.effective())
		}
	}
	// Invariant 4: rejected calls leave no trace.
	h, _ := l.Holder()
	e := l.Effective()
	_ = l.Acquire(999999, 0)
	_ = l.Acquire(h, 1)
	_ = l.Release(999999)
	if h2, _ := l.Holder(); h2 != h || l.Effective() != e {
		return fmt.Errorf("invariant4: rejected calls changed state")
	}
	return nil
}
