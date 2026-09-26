// Package ring holds the ordered virtual-node ring, clockwise successor
// lookup (with wrap-around), and node attach/detach. It depends on hashk.
package ring

import (
	"sort"
	"sync"
	"sync/atomic"

	"ontology/hashk"
)

// vnode is one virtual-node slot on the ring.
type vnode struct {
	pos  uint32
	node uint32
}

// Ring is an in-memory, concurrency-safe ordered set of virtual nodes.
type Ring struct {
	mu            sync.RWMutex
	vnodesPerNode int
	slots         []vnode // sorted by (pos, node)
	members       map[uint32]struct{}
	// lastChecks is non-exported: slots compared by the most recent Get.
	lastChecks atomic.Int64
}

// New creates an empty ring with v virtual nodes per real node.
func New(v int) *Ring {
	return &Ring{vnodesPerNode: v, members: make(map[uint32]struct{})}
}

// Add attaches all v virtual nodes of id. It validates membership before any
// mutation, so a duplicate id fails as a whole and leaves no trace.
func (r *Ring) Add(id uint32) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.members[id]; ok {
		return false
	}
	added := make([]vnode, 0, r.vnodesPerNode)
	for i := 0; i < r.vnodesPerNode; i++ {
		added = append(added, vnode{pos: hashk.VNodePos(id, i), node: id})
	}
	r.slots = append(r.slots, added...)
	sort.Slice(r.slots, func(a, b int) bool {
		if r.slots[a].pos != r.slots[b].pos {
			return r.slots[a].pos < r.slots[b].pos
		}
		return r.slots[a].node < r.slots[b].node
	})
	r.members[id] = struct{}{}
	return true
}

// Remove validates membership, then detaches every vnode of id in one pass.
func (r *Ring) Remove(id uint32) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.members[id]; !ok {
		return false
	}
	kept := r.slots[:0]
	for _, s := range r.slots {
		if s.node != id {
			kept = append(kept, s)
		}
	}
	r.slots = kept
	delete(r.members, id)
	return true
}

// successor returns owner via lower-bound binary search, counting each
// comparison. lo==n means every slot is smaller, so it wraps to slots[0].
func (r *Ring) successor(key uint32) uint32 {
	h := hashk.H(key)
	n := len(r.slots)
	lo, hi, checks := 0, n, 0
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		checks++
		if r.slots[mid].pos < h {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	r.lastChecks.Store(int64(checks))
	if lo == n {
		lo = 0 // wrap-around: clockwise first slot on the ring
	}
	return r.slots[lo].node
}

// Get returns the owner of key. False when the ring is empty.
func (r *Ring) Get(key uint32) (uint32, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.slots) == 0 {
		r.lastChecks.Store(0)
		return 0, false
	}
	return r.successor(key), true
}

// NodeCount reports attached real-node count.
func (r *Ring) NodeCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.members)
}

// VNodeCount reports current virtual-node count m.
func (r *Ring) VNodeCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.slots)
}

// LookupCostBounded reports whether the last Get compared at most limit slots.
// It exposes only a boolean, never the counter's numeric value.
func (r *Ring) LookupCostBounded(limit int) bool {
	return int(r.lastChecks.Load()) <= limit
}
