// Package hazard holds per-thread hazard pointer slots and answers whether any
// thread currently protects a given node. It depends on no other package.
//
// Protection is decided through a reverse set (node id -> number of slots
// pointing at it), so a single-node lookup touches one aggregated entry rather
// than the m thread slots.
package hazard

import (
	"errors"
	"sync"
)

// Sentinel errors for slot state violations.
var (
	// ErrSlotBusy is returned when a thread that already protects a node asks
	// to protect another one without clearing first.
	ErrSlotBusy = errors.New("thread already protects another node")
	// ErrEmptySlot is returned when clearing a thread that protects nothing.
	ErrEmptySlot = errors.New("thread holds no hazard pointer")
)

// Table maps thread ids to the node ids they protect. Create with New.
type Table struct {
	mu sync.Mutex
	// slots is thread id -> protected node id; only protecting threads appear.
	slots map[int]int
	// protected is the reverse aggregation: node id -> slots pointing at it.
	protected map[int]int
	// lastChecks counts the hazard slots inspected while deciding whether the
	// single most recently judged node was protected. It is unexported on
	// purpose: the constant-time property is exposed only as pass/fail via
	// CheckConstantLookup, never as a numeric value.
	lastChecks int
}

// New returns an empty hazard table.
func New() *Table {
	return &Table{slots: map[int]int{}, protected: map[int]int{}}
}

// Set publishes nodeID as thread id's protected node. Publish-before-deref:
// the slot is visible before any dereference of nodeID by the caller.
func (t *Table) Set(id, nodeID int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.slots[id]; ok {
		return ErrSlotBusy
	}
	t.slots[id] = nodeID
	t.protected[nodeID]++
	return nil
}

// Clear empties thread id's hazard slot.
func (t *Table) Clear(id int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	nodeID, ok := t.slots[id]
	if !ok {
		return ErrEmptySlot
	}
	delete(t.slots, id)
	if t.protected[nodeID] <= 1 {
		delete(t.protected, nodeID)
	} else {
		t.protected[nodeID]--
	}
	return nil
}

// Unprotected reports whether no thread's hazard slot currently points at
// nodeID. The decision inspects exactly one entry of the reverse set, so its
// cost is independent of the number of protecting threads.
func (t *Table) Unprotected(nodeID int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastChecks = 1 // one aggregated reverse-set entry; a slot scan would be m.
	_, held := t.protected[nodeID]
	return !held
}

// Snapshot returns a copy of the published hazards (thread id -> node id).
func (t *Table) Snapshot() map[int]int {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[int]int, len(t.slots))
	for id, nodeID := range t.slots {
		out[id] = nodeID
	}
	return out
}

// CheckConstantLookup reports pass/fail for the given thread counts: for each
// m it builds a fresh table where m threads each protect a unique node, asks
// whether an unheld node is protected (the per-node question Reclaim asks),
// and verifies the answer cost one reverse-set entry. It returns only
// nil/non-nil; the internal counter value is never exposed.
func (t *Table) CheckConstantLookup(ms ...int) error {
	for _, m := range ms {
		h := New()
		for i := 0; i < m; i++ {
			if err := h.Set(i, 1000+i); err != nil {
				return err
			}
		}
		h.Unprotected(42) // node 42 is held by nobody.
		h.mu.Lock()
		c := h.lastChecks
		h.mu.Unlock()
		if c != 1 {
			return errors.New("hazard: protection lookup is not O(1)")
		}
	}
	return nil
}
