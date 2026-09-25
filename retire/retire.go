// Package retire keeps the retired-node list and performs safe reclamation:
// a retired node is freed only when the hazard table points at it nowhere.
// It depends only on the hazard package.
package retire

import (
	"errors"
	"sync"

	"ontology/hazard"
)

// Sentinel errors for retired-list violations.
var (
	// ErrInvalidNode is returned for node ids that cannot name a node.
	ErrInvalidNode = errors.New("invalid node id")
	// ErrAlreadyRetired is returned when retiring a node already retired.
	ErrAlreadyRetired = errors.New("node already retired")
)

// List is an ordered retired-node list coupled to a hazard table.
type List struct {
	h *hazard.Table

	mu    sync.Mutex
	order []int // retirement order, kept stable across Reclaims
	set   map[int]struct{}
}

// New returns an empty retired list backed by h.
func New(h *hazard.Table) *List {
	return &List{h: h, set: map[int]struct{}{}}
}

// Retire places nodeID on the retired list. nodeID <= 0 or a node already
// retired is rejected; either rejection leaves the list untouched.
func (l *List) Retire(nodeID int) error {
	if nodeID <= 0 {
		return ErrInvalidNode
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.set[nodeID]; ok {
		return ErrAlreadyRetired
	}
	l.set[nodeID] = struct{}{}
	l.order = append(l.order, nodeID)
	return nil
}

// Reclaim frees every retired node that no hazard pointer points at and
// returns their ids in retirement order; still-protected nodes stay on the
// list and are judged again on the next call.
//
// Safety: the hazard verdict for each node comes from Table.Unprotected,
// which decides under the hazard table's lock; anything returned was, at the
// moment of that verdict, pointed at by no thread.
func (l *List) Reclaim() []int {
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.order[:0]
	var freed []int
	for _, nodeID := range l.order {
		// Lock order is always list -> hazard; nothing takes them in reverse.
		if l.h.Unprotected(nodeID) {
			delete(l.set, nodeID)
			freed = append(freed, nodeID)
		} else {
			kept = append(kept, nodeID)
		}
	}
	l.order = kept
	return freed
}

// Snapshot returns a copy of the currently retired node ids in list order.
func (l *List) Snapshot() []int {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]int, len(l.order))
	copy(out, l.order)
	return out
}
