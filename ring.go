// Package ontology implements a consistent hash ring with virtual nodes.
//
// Nodes (string IDs) each own vnodes positions on a uint64 hash circle.
// Locate maps a key to the first virtual node at or after the key's hash,
// wrapping around to the smallest position when the hash exceeds every
// virtual node. Adding or removing a node only moves the keys that hash
// into that node's arcs, so relocation is bounded near 1/(n+1) on add.
package ontology

import (
	"sort"
	"sync"
)

// point is one virtual node on the ring.
type point struct {
	hash uint64
	node string
}

// Point is an exported snapshot of a virtual node, for inspection.
type Point struct {
	Hash uint64
	Node string
}

// Ring is a consistent hash ring. It is safe for concurrent use.
type Ring struct {
	mu     sync.RWMutex
	vnodes map[string]int // node ID -> vnode count
	points []point        // sorted by (hash, node)
}

// New returns an empty ring.
func New() *Ring {
	return &Ring{vnodes: make(map[string]int)}
}

// Add places id on the ring with vn virtual nodes. It fails with
// ErrInvalidVnodes if vn <= 0, and with ErrNodeExists (leaving the ring
// untouched) if id is already present.
func (r *Ring) Add(id string, vn int) error {
	if vn <= 0 {
		return ErrInvalidVnodes
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.vnodes[id]; ok {
		return ErrNodeExists
	}
	for i := 0; i < vn; i++ {
		r.points = append(r.points, point{hash: vnodeHash(id, i), node: id})
	}
	r.vnodes[id] = vn
	sortPoints(r.points)
	return nil
}

// Remove deletes id and all its virtual nodes. It fails with
// ErrNodeNotFound if id is not on the ring.
func (r *Ring) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.vnodes[id]; !ok {
		return ErrNodeNotFound
	}
	delete(r.vnodes, id)
	kept := r.points[:0]
	for _, p := range r.points {
		if p.node != id {
			kept = append(kept, p)
		}
	}
	r.points = kept
	return nil
}

// Locate returns the node owning key, or ErrEmptyRing if the ring is
// empty. Hashes past the largest virtual node wrap to the smallest one.
func (r *Ring) Locate(key string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.points) == 0 {
		return "", ErrEmptyRing
	}
	return r.locateHash(HashKey(key)), nil
}

// locateHash finds the owner of hash h. Caller must hold the lock and
// the ring must be non-empty.
func (r *Ring) locateHash(h uint64) string {
	i := sort.Search(len(r.points), func(i int) bool { return r.points[i].hash >= h })
	if i == len(r.points) {
		i = 0 // wrap around to the smallest position
	}
	return r.points[i].node
}

// Len reports how many distinct nodes are on the ring.
func (r *Ring) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.vnodes)
}

// Points returns a snapshot of all virtual nodes, sorted by (hash, node).
func (r *Ring) Points() []Point {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Point, len(r.points))
	for i, p := range r.points {
		out[i] = Point{Hash: p.hash, Node: p.node}
	}
	return out
}

// sortPoints orders points by hash; equal hashes (collisions) are broken
// by lexicographic node ID, independent of insertion order.
func sortPoints(ps []point) {
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].hash != ps[j].hash {
			return ps[i].hash < ps[j].hash
		}
		return ps[i].node < ps[j].node
	})
}
