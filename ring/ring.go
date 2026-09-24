// Package ring implements a consistent hash ring with virtual nodes.
// All state lives in process memory; Locate is safe for concurrent use.
package ring

import (
	"sort"
	"sync"
)

// Point is one virtual node position on the ring.
type Point struct {
	Hash uint64
	Node string
}

// Ring is a consistent hash ring guarded by a RWMutex.
type Ring struct {
	mu     sync.RWMutex
	vnodes map[string]int
	points []Point // sorted by (Hash, Node)
}

// New returns an empty ring.
func New() *Ring {
	return &Ring{vnodes: make(map[string]int)}
}

// Add places node on the ring with vnodes virtual nodes.
func (r *Ring) Add(node string, vnodes int) error {
	if vnodes <= 0 {
		return ErrInvalidVnodes
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.vnodes[node]; ok {
		return ErrNodeExists
	}
	r.vnodes[node] = vnodes
	for i := 0; i < vnodes; i++ {
		r.points = append(r.points, Point{Hash: vnodeHash(node, i), Node: node})
	}
	r.sortPoints()
	return nil
}

// Remove drops node and all its virtual nodes from the ring.
func (r *Ring) Remove(node string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.vnodes[node]; !ok {
		return ErrNodeNotFound
	}
	delete(r.vnodes, node)
	kept := r.points[:0]
	for _, p := range r.points {
		if p.Node != node {
			kept = append(kept, p)
		}
	}
	r.points = kept
	return nil
}

// Locate returns the node owning key, or ErrEmptyRing on an empty ring.
func (r *Ring) Locate(key string) (string, error) {
	return r.LocateHash(HashKey(key))
}

// LocateHash returns the node owning ring position h. Positions past the
// largest virtual node wrap around to the smallest one.
func (r *Ring) LocateHash(h uint64) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.locateHashLocked(h)
}

func (r *Ring) locateHashLocked(h uint64) (string, error) {
	if len(r.points) == 0 {
		return "", ErrEmptyRing
	}
	idx := sort.Search(len(r.points), func(i int) bool {
		return r.points[i].Hash >= h
	})
	if idx == len(r.points) {
		idx = 0 // wrap around to the smallest position
	}
	return r.points[idx].Node, nil
}

// Has reports whether node is currently on the ring.
func (r *Ring) Has(node string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.vnodes[node]
	return ok
}

// Len returns the number of distinct nodes on the ring.
func (r *Ring) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.vnodes)
}

// Points returns a copy of the sorted virtual node positions.
func (r *Ring) Points() []Point {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Point, len(r.points))
	copy(out, r.points)
	return out
}

// sortPoints orders by hash; equal hashes (collisions between different
// nodes) are broken by lexicographic node ID, independent of insert order.
func (r *Ring) sortPoints() {
	sort.Slice(r.points, func(i, j int) bool {
		if r.points[i].Hash != r.points[j].Hash {
			return r.points[i].Hash < r.points[j].Hash
		}
		return r.points[i].Node < r.points[j].Node
	})
}

// addPoint inserts one raw point; used by tests to hand-build rings.
func (r *Ring) addPoint(hash uint64, node string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.vnodes[node]++
	r.points = append(r.points, Point{Hash: hash, Node: node})
	r.sortPoints()
}
