// Package route performs dedicated-shard allocation, migration timing and
// the second-level routing decision on top of a shard.Tracker.
//
// Routing rule: a hot key's events land on its dedicated shard; every other
// event lands on its base shard. The single event whose base count reaches
// the threshold T is the migration trigger: it is counted on the BASE shard
// first, and only afterwards is the key marked hot and bound to a freshly
// allocated dedicated shard, so the next event of that key is the first one
// to land there. Nothing is ever counted twice or dropped.
package route

import (
	"sync"

	"ontology/shard"
)

// Router serializes all access to its shard.Tracker.
type Router struct {
	mu sync.RWMutex
	tr *shard.Tracker

	// dedicatedCount is the number of dedicated shards already handed out;
	// the next dedicated shard number is S + dedicatedCount.
	dedicatedCount int
}

// NewRouter wraps tr.
func NewRouter(tr *shard.Tracker) *Router {
	return &Router{tr: tr}
}

// Outcome is the result of routing one event.
type Outcome struct {
	Shard    int  // shard the event actually landed on
	Migrated bool // true only for the threshold-reaching trigger event
}

// Apply routes a single pre-validated event under the write lock and
// returns where it landed. Exactly one shard is incremented per call.
func (r *Router) Apply(key string, base int) Outcome {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.tr.Migrated(key) {
		// Already hot: every post-migration event lands on the dedicated shard.
		s, _ := r.tr.DedicatedOf(key)
		r.tr.AddShard(s)
		return Outcome{Shard: s}
	}

	// Pre-migration: count on the base shard first.
	n := r.tr.AddBase(key, base)
	if n >= int64(r.tr.T) {
		// This event just reached T: it stays on base; allocate the
		// dedicated shard only now, so the next event is the first to use it.
		s := r.tr.S + r.dedicatedCount
		r.dedicatedCount++
		r.tr.CommitMigration(key, s)
		return Outcome{Shard: base, Migrated: true}
	}
	return Outcome{Shard: base}
}

// Snapshot returns a copy of cnt ordered by shard number.
func (r *Router) Snapshot() []int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tr.Snapshot()
}

// Sum returns the total count across all shards.
func (r *Router) Sum() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tr.Sum()
}

// IsHot reports whether key is a permanently hot key.
func (r *Router) IsHot(key string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.tr.DedicatedOf(key)
	return ok
}

// Dedicated returns key's dedicated shard once it has migrated.
func (r *Router) Dedicated(key string) (int, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tr.DedicatedOf(key)
}
