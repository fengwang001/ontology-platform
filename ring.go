package ontology

import (
	"sort"
	"sync"
)

// vnode is one virtual node placed at a position on the ring.
type vnode struct {
	pos  uint64
	node string
}

// Ring is an in-memory consistent hash ring.
//
// Each real node owns vnodes virtual nodes spread around a 64-bit key space.
// Keys map to the first vnode encountered clockwise from the key's hash;
// positions past the largest vnode wrap to the smallest vnode.
//
// Ring is safe for concurrent use: Locate takes a read lock while Add and
// Remove take a write lock.
type Ring struct {
	mu    sync.RWMutex
	ring  []vnode        // sorted by (pos, node)
	nodes map[string]int // node ID -> vnode count
}

// New returns an empty ring.
func New() *Ring {
	return &Ring{nodes: make(map[string]int)}
}

// Locate returns the node that owns key.
func (r *Ring) Locate(key string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.ring) == 0 {
		return "", ErrEmptyRing
	}
	h := hashString(key)
	i := sort.Search(len(r.ring), func(i int) bool {
		return r.ring[i].pos >= h
	})
	if i == len(r.ring) {
		// Wrap-around: hashes past the largest position belong to the
		// smallest vnode on the ring.
		i = 0
	}
	return r.ring[i].node, nil
}

// Nodes returns the currently present node IDs in sorted order.
func (r *Ring) Nodes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.nodes))
	for id := range r.nodes {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Contains reports whether id is currently a member of the ring.
func (r *Ring) Contains(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.nodes[id]
	return ok
}
