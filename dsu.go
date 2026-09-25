// Package ontology provides an in-memory equivalence-class merger
// (disjoint-set union) over string IDs.
//
// The representative of a class is always the lexicographically smallest
// ID in that class, independent of union order. Internally it uses union
// by rank and path compression, so both Union and Find run in amortized
// near-constant time. The zero value is ready to use and all methods are
// safe for concurrent use.
package ontology

import (
	"errors"
	"sync"
)

// ErrUnknownElement is returned by read operations that reference an ID
// which has never been added to the set (via Add or Union). Union never
// returns it: Union implicitly creates unknown IDs instead.
var ErrUnknownElement = errors.New("ontology: unknown element")

// DisjointSet merges string IDs into equivalence classes.
type DisjointSet struct {
	mu     sync.Mutex
	parent map[string]string
	rank   map[string]int
	min    map[string]string // root -> lexicographically smallest member
	count  int
}

func (ds *DisjointSet) initLocked() {
	if ds.parent == nil {
		ds.parent = make(map[string]string)
		ds.rank = make(map[string]int)
		ds.min = make(map[string]string)
	}
}

// Add explicitly creates id as a singleton class. It is idempotent:
// adding an existing ID is a no-op and never fails. The empty string is
// a legal ID.
func (ds *DisjointSet) Add(id string) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	ds.addLocked(id)
}

func (ds *DisjointSet) addLocked(id string) {
	ds.initLocked()
	if _, ok := ds.parent[id]; ok {
		return
	}
	ds.parent[id] = id
	ds.rank[id] = 0
	ds.min[id] = id
	ds.count++
}

// findRootLocked returns the tree root of x and the number of parent
// pointers traversed to reach it. It performs path compression: every
// node visited on the way up is re-linked directly to the root.
// The caller must hold the lock and must ensure x exists.
func (ds *DisjointSet) findRootLocked(x string) (root string, hops int) {
	root = x
	for ds.parent[root] != root {
		root = ds.parent[root]
		hops++
	}
	for ds.parent[x] != root {
		next := ds.parent[x]
		ds.parent[x] = root
		x = next
	}
	return root, hops
}

// Union merges the classes of a and b into one. Unknown IDs are created
// implicitly. Unioning two elements that are already in the same class
// is a no-op.
func (ds *DisjointSet) Union(a, b string) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	ds.addLocked(a)
	ds.addLocked(b)
	ra, _ := ds.findRootLocked(a)
	rb, _ := ds.findRootLocked(b)
	if ra == rb {
		return
	}
	// Union by rank: the shallower tree hangs under the deeper one.
	if ds.rank[ra] < ds.rank[rb] {
		ra, rb = rb, ra
	}
	ds.parent[rb] = ra
	if ds.rank[ra] == ds.rank[rb] {
		ds.rank[ra]++
	}
	// The representative is decoupled from the tree root: the new root
	// simply remembers the smaller of the two class minimums.
	if ds.min[rb] < ds.min[ra] {
		ds.min[ra] = ds.min[rb]
	}
	delete(ds.min, rb)
	ds.count--
}

// Find returns the representative of x's class, which is always the
// lexicographically smallest ID in the class. It returns
// ErrUnknownElement if x has never been added; Find never creates IDs.
func (ds *DisjointSet) Find(x string) (string, error) {
	rep, _, err := ds.FindWithHops(x)
	return rep, err
}

// FindWithHops behaves like Find and additionally reports how many
// parent pointers the lookup traversed before path compression. It
// exists for tests and diagnostics that verify path compression.
func (ds *DisjointSet) FindWithHops(x string) (rep string, hops int, err error) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	ds.initLocked()
	if _, ok := ds.parent[x]; !ok {
		return "", 0, ErrUnknownElement
	}
	root, hops := ds.findRootLocked(x)
	return ds.min[root], hops, nil
}
