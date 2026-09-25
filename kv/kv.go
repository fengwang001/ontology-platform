// Package kv holds a single replicated key-value replica and the master's
// per-key monotonic version counter. It depends on no other package.
package kv

import "sync"

// Entry is one stored value with its global per-key version. Ver == 0 means
// the key does not exist.
type Entry struct {
	Val string
	Ver int64
}

// AtLeast reports whether the entry is at least version want. A replica whose
// entry satisfies AtLeast(sessWriteVer) is safe to serve a read-your-writes
// read from.
func (e Entry) AtLeast(want int64) bool { return e.Ver >= want }

// Replica is one copy of the data: R0 (master) or R1/R2 (followers).
type Replica struct {
	mu sync.RWMutex
	m  map[string]Entry
}

// NewReplica returns an empty replica.
func NewReplica() *Replica { return &Replica{m: map[string]Entry{}} }

// Get returns the entry and presence for key.
func (r *Replica) Get(key string) (Entry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.m[key]
	return e, ok
}

// Put stores e for key. Used by the master to apply a write.
func (r *Replica) Put(key string, e Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[key] = e
}

// Snapshot returns a deep copy of the whole replica state.
func (r *Replica) Snapshot() map[string]Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]Entry, len(r.m))
	for k, e := range r.m {
		out[k] = e
	}
	return out
}

// Load replaces the whole state with a deep copy of src (follower Sync).
func (r *Replica) Load(src map[string]Entry) {
	cp := make(map[string]Entry, len(src))
	for k, e := range src {
		cp[k] = e
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m = cp
}

// Master is the authoritative replica R0; it owns ver[key].
type Master struct {
	*Replica
	ver map[string]int64
}

// NewMaster returns R0.
func NewMaster() *Master {
	return &Master{Replica: NewReplica(), ver: map[string]int64{}}
}

// Advance allocates the next version for key, stores (val, v) on R0 and
// returns v. Versions only ever increase.
func (m *Master) Advance(key, val string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	v := m.ver[key] + 1
	m.ver[key] = v
	m.m[key] = Entry{Val: val, Ver: v}
	return v
}

// Version returns the current authoritative version for key (0 if never
// written), without changing anything.
func (m *Master) Version(key string) int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.ver[key]
}
