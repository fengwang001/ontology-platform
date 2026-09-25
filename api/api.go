// Package api is the public, concurrency-safe face of the 2-3 tree.
package api

import (
	"errors"
	"slices"
	"sync"

	"ontology/bnode"
	"ontology/btree"
)

var (
	ErrDuplicate = btree.ErrDuplicate
	ErrNotFound  = btree.ErrNotFound
	ErrFull      = btree.ErrFull
)

// Tree is a thread-safe 2-3 tree of distinct int keys.
type Tree struct {
	mu   sync.Mutex
	t    *btree.Tree
	live map[int]bool // inserted and not yet deleted (SelfCheck reference set)
}

// New creates an empty tree holding at most maxKeys keys.
func New(maxKeys int) (*Tree, error) {
	if maxKeys < 1 {
		return nil, errors.New("api: maxKeys must be positive")
	}
	return &Tree{t: btree.New(maxKeys), live: map[int]bool{}}, nil
}

func (a *Tree) Insert(k int) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, err := a.t.Insert(k)
	if err == nil {
		a.live[k] = true
	}
	return c, err
}
func (a *Tree) Delete(k int) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, err := a.t.Delete(k)
	if err == nil {
		delete(a.live, k)
	}
	return c, err
}
func (a *Tree) Search(k int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.t.Search(k)
}
func (a *Tree) OrderedKeys() []int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.t.Root.Inorder(nil)
}

// Height returns the number of levels; an empty tree has height 1.
func (a *Tree) Height() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	h := 1
	for n := a.t.Root; !n.Leaf(); n = n.Children[0] {
		h++
	}
	return h
}

// SelfCheck verifies the four invariants: on the current tree (structure,
// ordering, search-vs-reference) and via a built-in operation sequence
// (rejected operations leave no trace). Nil means all hold.
func (a *Tree) SelfCheck() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := checkNode(a.t.Root, true, 0, new(int)); err != nil {
		return err
	}
	keys := a.t.Root.Inorder(nil)
	if !slices.IsSorted(keys) || len(keys) != len(a.live) {
		return errors.New("api: ordered keys unsorted or count mismatch")
	}
	for _, k := range keys {
		if !a.live[k] || !a.t.Search(k) {
			return errors.New("api: tree key missing from live set or search")
		}
	}
	for _, k := range []int{-1, 1 << 30} { // absent probes vs binary search
		_, found := slices.BinarySearch(keys, k)
		if a.t.Search(k) != found {
			return errors.New("api: search disagrees with binary search")
		}
	}
	return selfSeq()
}

// selfSeq runs the built-in sequence verifying rejected operations are
// distinct, detectable, and leave structure and costs untouched.
func selfSeq() error {
	tr := btree.New(4)
	for _, k := range []int{3, 1, 4, 2} {
		if _, err := tr.Insert(k); err != nil {
			return err
		}
	}
	before := tr.Root.Inorder(nil)
	_, e1 := tr.Insert(2) // duplicate
	_, e2 := tr.Delete(9) // not found
	_, e3 := tr.Insert(5) // full
	if !errors.Is(e1, btree.ErrDuplicate) || !errors.Is(e2, btree.ErrNotFound) ||
		!errors.Is(e3, btree.ErrFull) || e1 == e2 || e2 == e3 || e1 == e3 {
		return errors.New("api: rejection errors not distinct sentinels")
	}
	if !slices.Equal(tr.Root.Inorder(nil), before) || tr.N != 4 {
		return errors.New("api: rejected operation mutated the tree")
	}
	return nil
}

// checkNode verifies key counts, child counts and uniform leaf depth.
func checkNode(n *bnode.Node, root bool, depth int, leafDepth *int) error {
	if len(n.Keys) > 2 || (!root && len(n.Keys) == 0) {
		return errors.New("api: node key count out of range")
	}
	if !slices.IsSorted(n.Keys) {
		return errors.New("api: node keys unsorted")
	}
	if n.Leaf() {
		if *leafDepth == 0 {
			*leafDepth = depth + 1
		}
		if *leafDepth != depth+1 {
			return errors.New("api: leaves at different depths")
		}
		return nil
	}
	if len(n.Children) != len(n.Keys)+1 {
		return errors.New("api: child count != keys+1")
	}
	for _, c := range n.Children {
		if err := checkNode(c, false, depth+1, leafDepth); err != nil {
			return err
		}
	}
	return nil
}
