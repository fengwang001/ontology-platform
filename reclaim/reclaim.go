// Package reclaim advances the garbage-collection watermark and manages the
// incremental set of keys that might hold reclaimable versions.
//
// It depends only on txid and snapshot. It never touches version chains:
// the store injects a PruneFunc, which keeps the dependency direction
// reclaim -> {txid, snapshot} one-way.
package reclaim

import (
	"sync"

	"ontology/snapshot"
	"ontology/txid"
)

// PruneFunc prunes one candidate key below watermark and reports how many
// chain versions were actually examined. The store implements it with
// version.Chain.PruneBelow.
type PruneFunc func(key string, watermark txid.ID) (examined int)

// Reclaimer owns the monotonic watermark and the candidate queue.
//
// Incremental strategy: a key enters the queue only when one of its versions
// becomes shadowed by a *newly committed* newer version (the only moment at
// which reclaimability can arise). Keys that merely received their first
// version, or whose later versions are still pending, never enter it. So a
// reclaim pass visits only keys where garbage can exist; untouched keys are
// never scanned regardless of how many keys the store holds.
type Reclaimer struct {
	reg *snapshot.Registry

	mu        sync.Mutex
	watermark txid.ID
	queue     []string
	queued    map[string]struct{}

	// examinedLast is the number of chain versions examined by the most
	// recent Advance. Unexported on purpose; accessor below.
	examinedLast int
	// examinedTotal accumulates examined counts across all passes.
	examinedTotal int
}

// New creates a Reclaimer bound to the snapshot registry.
func New(reg *snapshot.Registry) *Reclaimer {
	return &Reclaimer{reg: reg, queued: make(map[string]struct{})}
}

// Watermark returns the current (monotonic) safe-reclaim upper bound.
// A version with commit < Watermark is invisible to no active snapshot.
func (r *Reclaimer) Watermark() txid.ID {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.watermark
}

// Notify records that key may have gained a shadowed version. Duplicate
// notifications while a key is already queued are coalesced.
func (r *Reclaimer) Notify(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.queued[key]; ok {
		return
	}
	r.queued[key] = struct{}{}
	r.queue = append(r.queue, key)
}

// Queued reports how many candidate keys await examination.
func (r *Reclaimer) Queued() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.queue)
}

// ExaminedLast returns versions examined during the last Advance.
func (r *Reclaimer) ExaminedLast() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.examinedLast
}

// ExaminedTotal returns versions examined across all advances.
func (r *Reclaimer) ExaminedTotal() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.examinedTotal
}
