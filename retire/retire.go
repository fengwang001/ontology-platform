// Package retire holds the retirement list and decides when retired nodes may
// be freed. It depends on epoch (one-directional: retire -> epoch only).
package retire

import (
	"errors"
	"sync"

	"ontology/epoch"
)

// Sentinel errors, distinct from each other and from epoch's.
var (
	ErrInvalidNode    = errors.New("retire: node id must be > 0")
	ErrAlreadyRetired = errors.New("retire: node is already retired")
)

// Item is one retired node stamped with the global epoch at retirement.
type Item struct {
	Node  int
	Epoch int64
}

// List is an ordered retirement list backed by an epoch registry.
type List struct {
	mu sync.Mutex

	reg     *epoch.Registry
	pending []Item       // retirement order
	member  map[int]bool // node -> present in pending
}

// New creates a retirement list over the given epoch registry.
func New(reg *epoch.Registry) *List {
	return &List{reg: reg, member: map[int]bool{}}
}

// Retire stamps node with the current epoch and appends it. Validation happens
// before any mutation, so a rejected call leaves no trace.
func (l *List) Retire(node int) error {
	if node <= 0 {
		return ErrInvalidNode
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.member[node] {
		return ErrAlreadyRetired
	}
	l.pending = append(l.pending, Item{Node: node, Epoch: l.reg.G()})
	l.member[node] = true
	return nil
}

// Reclaim frees every pending node whose retirement epoch is strictly less than
// the current minimum active epoch. With no active threads the minimum is
// +infinity, so all pending nodes are freed. Freed node ids are returned in
// retirement order.
func (l *List) Reclaim() []int {
	minE, hasActive := l.reg.MinActive()
	l.mu.Lock()
	defer l.mu.Unlock()

	kept := l.pending[:0] // reuse backing array; we rebuild in one pass
	freed := []int{}
	for _, it := range l.pending {
		safe := !hasActive || it.Epoch < minE
		if safe {
			freed = append(freed, it.Node)
			delete(l.member, it.Node)
		} else {
			kept = append(kept, it)
		}
	}
	l.pending = kept
	return freed
}

// Pending returns a copy of the still-retired items (retirement order).
func (l *List) Pending() []Item {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Item, len(l.pending))
	copy(out, l.pending)
	return out
}

// IsRetired reports whether node is still waiting to be reclaimed.
func (l *List) IsRetired(node int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.member[node]
}
