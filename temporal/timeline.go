package temporal

import (
	"sort"
	"sync"
	"sync/atomic"
)

type atomicInstant struct{ v atomic.Int64 }

func (a *atomicInstant) get() Instant  { return Instant(a.v.Load()) }
func (a *atomicInstant) set(t Instant) { a.v.Store(int64(t)) }

type atomicUint64 struct{ v atomic.Uint64 }

func (a *atomicUint64) next() uint64 { return a.v.Add(1) }

// entry is one versioned record on a key's timeline. Entries are immutable
// once appended; lookup at instant t picks the last entry with at <= t.
type entry[V any] struct {
	at    Instant
	value V
	alive bool
}

// timeline is the append-only version history of one key. Reads use an
// atomic snapshot of the backing slice pointer; writers install a new
// slice (copy-on-write), so long-running readers never block writers and
// always observe one consistent length.
type timeline[V any] struct {
	ptr atomic.Pointer[[]entry[V]]
}

func (tl *timeline[V]) load() []entry[V] {
	p := tl.ptr.Load()
	if p == nil {
		return nil
	}
	return *p
}

func (tl *timeline[V]) appendLocked(e entry[V]) {
	old := tl.load()
	next := make([]entry[V], len(old)+1)
	copy(next, old)
	next[len(old)] = e
	tl.ptr.Store(&next)
}

// at returns the entry in force at instant t and whether any entry covers t.
// found is false when the key had no record at or before t.
func (tl *timeline[V]) at(t Instant) (entry[V], bool) {
	es := tl.load()
	i := sort.Search(len(es), func(i int) bool { return es[i].at > t }) - 1
	if i < 0 {
		return entry[V]{}, false
	}
	return es[i], true
}

// versioned is a key->timeline map kept under a write mutex; reads lock
// nothing and take the *timeline pointer directly from the map (map values
// are only ever inserted, never mutated or deleted).
type versioned[K comparable, V any] struct {
	mu sync.RWMutex
	m  map[K]*timeline[V]
}

func newVersioned[K comparable, V any]() *versioned[K, V] {
	return &versioned[K, V]{m: make(map[K]*timeline[V])}
}

// get fetches a key's timeline. It takes the read lock only for the pointer
// lookup; once the *timeline is returned, all reads are lock-free, so a
// long-running traversal never blocks a committer.
func (v *versioned[K, V]) get(key K) *timeline[V] {
	v.mu.RLock()
	tl := v.m[key]
	v.mu.RUnlock()
	return tl
}

// getOrCreateLocked returns the timeline, creating it under the write lock.
func (v *versioned[K, V]) getOrCreateLocked(key K) *timeline[V] {
	v.mu.Lock()
	defer v.mu.Unlock()
	tl := v.m[key]
	if tl == nil {
		tl = &timeline[V]{}
		v.m[key] = tl
	}
	return tl
}

// rangeLocked iterates all keys while holding the read lock. The callback
// must not escape the *timeline into use after returning without reading it
// first; commits use it only to count current degrees.
func (v *versioned[K, V]) rangeLocked(fn func(K, *timeline[V])) {
	v.mu.RLock()
	for k, tl := range v.m {
		fn(k, tl)
	}
	v.mu.RUnlock()
}

// rangeWriteLocked iterates all keys WITHOUT locking; the caller must
// already hold the write lock (used during a serialized commit, where a
// re-entrant read lock on Go's RWMutex would deadlock).
func (v *versioned[K, V]) rangeWriteLocked(fn func(K, *timeline[V])) {
	for k, tl := range v.m {
		fn(k, tl)
	}
}

// rootBucket is one object's versioned adjacency index: an append-only
// sequence of committed persistent-treap roots, published atomically.
type rootBucket struct {
	ptr atomic.Pointer[[]adjRoot]
}

func (b *rootBucket) load() []adjRoot {
	p := b.ptr.Load()
	if p == nil {
		return nil
	}
	return *p
}

func (b *rootBucket) store(roots []adjRoot) { b.ptr.Store(&roots) }

// adjacency maps each object id to its out- or in-bucket. Buckets are only
// inserted, never deleted.
type adjacency struct {
	mu sync.RWMutex
	m  map[ObjectID]*rootBucket
}

func newAdjacency() *adjacency { return &adjacency{m: map[ObjectID]*rootBucket{}} }

func (a *adjacency) get(id ObjectID) *rootBucket {
	a.mu.RLock()
	b := a.m[id]
	a.mu.RUnlock()
	return b
}

func (a *adjacency) getOrCreateLocked(id ObjectID) *rootBucket {
	a.mu.Lock()
	defer a.mu.Unlock()
	b := a.m[id]
	if b == nil {
		b = &rootBucket{}
		a.m[id] = b
	}
	return b
}
