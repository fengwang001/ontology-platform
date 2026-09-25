// Package epoch tracks the global epoch and active (critical-section) threads.
// It depends on nothing else in the project.
package epoch

import (
	"errors"
	"sync"
)

// Sentinel errors (judgeable via errors.Is).
var (
	ErrDuplicateEnter = errors.New("epoch: thread already in a critical section")
	ErrNotActive      = errors.New("epoch: thread is not in a critical section")
)

// Registry maps active thread id -> its announced epoch and keeps a per-epoch
// active count so the minimum active epoch is found in O(1) amortized time
// without scanning every thread.
type Registry struct {
	mu sync.Mutex

	g      int64         // global epoch, starts at 0
	active map[int]int64 // thread id -> announced epoch
	counts map[int64]int // epoch -> number of active threads announced at it
	nAct   int           // len(active)
	floor  int64         // smallest epoch not yet known empty

	// lastMinScannedThreads counts thread records inspected by the most recent
	// minimum-active-epoch computation. The per-epoch-count algorithm only ever
	// inspects epoch buckets, never thread records, so it is always 0 here
	// (a whole-table scan over m threads would inspect m). Unexported on purpose.
	lastMinScannedThreads int
}

// New returns an empty registry with G == 0.
func New() *Registry {
	return &Registry{active: map[int]int64{}, counts: map[int64]int{}}
}

// Enter announces thread id as active at the current global epoch.
func (r *Registry) Enter(id int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.active[id]; ok {
		return ErrDuplicateEnter
	}
	r.active[id] = r.g
	r.counts[r.g]++
	r.nAct++
	return nil
}

// Exit removes thread id from the active set.
func (r *Registry) Exit(id int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.active[id]
	if !ok {
		return ErrNotActive
	}
	delete(r.active, id)
	r.nAct--
	if r.counts[e]--; r.counts[e] == 0 {
		delete(r.counts, e) // keep counts sparse so the floor walk sees emptiness
	}
	return nil
}

// Advance bumps the global epoch by one.
func (r *Registry) Advance() {
	r.mu.Lock()
	r.g++
	r.mu.Unlock()
}

// G returns the current global epoch.
func (r *Registry) G() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.g
}

// MinActive returns the smallest epoch at which some thread is active.
// ok is false when no thread is active (caller treats it as +infinity).
func (r *Registry) MinActive() (e int64, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastMinScannedThreads = 0 // count buckets are inspected, zero thread records
	if r.nAct == 0 {
		return 0, false
	}
	for r.counts[r.floor] == 0 {
		r.floor++ // each epoch is crossed at most once over the registry's life
	}
	return r.floor, true
}

// Active returns a copy of the id -> announced-epoch map.
func (r *Registry) Active() map[int]int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[int]int64, len(r.active))
	for id, e := range r.active {
		out[id] = e
	}
	return out
}
