// Package dedupe implements a bounded FIFO dedup table mapping uids to
// results. When full, Add evicts the oldest registered uid. A lookup hit
// does not refresh the entry's position.
package dedupe

import "errors"

// ErrBadCapacity is returned by New when k is outside [1, 1e6].
var ErrBadCapacity = errors.New("dedupe: capacity out of range")

// MaxCapacity is the largest allowed table capacity.
const MaxCapacity = 1_000_000

// Table is a bounded uid -> V map with FIFO eviction on first registration.
type Table[V any] struct {
	cap   int
	items map[string]V
	order []string // uids in registration order, oldest first
	head  int      // index of oldest live entry in order
}

// New returns a table holding at most k entries.
func New[V any](k int) (*Table[V], error) {
	if k < 1 || k > MaxCapacity {
		return nil, ErrBadCapacity
	}
	return &Table[V]{cap: k, items: make(map[string]V)}, nil
}

// Len returns the number of live entries.
func (t *Table[V]) Len() int { return len(t.items) }

// Get returns the value registered for uid. It does not refresh position.
func (t *Table[V]) Get(uid string) (V, bool) {
	v, ok := t.items[uid]
	return v, ok
}

// Add registers uid -> v. If the table is full, the oldest registered uid
// is evicted first. Adding an existing uid replaces the value in place
// without changing its position.
func (t *Table[V]) Add(uid string, v V) {
	if _, ok := t.items[uid]; ok {
		t.items[uid] = v
		return
	}
	if len(t.items) >= t.cap {
		t.evictOldest()
	}
	t.items[uid] = v
	t.order = append(t.order, uid)
}

// evictOldest removes the earliest registered live entry.
func (t *Table[V]) evictOldest() {
	oldest := t.order[t.head]
	t.head++
	delete(t.items, oldest)
	// Compact the slice once the consumed prefix dominates it.
	if t.head >= len(t.order)/2 {
		t.order = append([]string(nil), t.order[t.head:]...)
		t.head = 0
	}
}

// Contains reports whether uid is registered.
func (t *Table[V]) Contains(uid string) bool {
	_, ok := t.items[uid]
	return ok
}
