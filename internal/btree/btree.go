// Package btree implements order-3 B-tree (2-3 tree) maintenance: splits,
// borrows, merges, cost counting and lookup; depends only on internal/bnode.
// Methods are concurrency-safe and api is a thin facade over this package.
package btree

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/internal/bnode"
)

var (
	ErrDuplicateKey = errors.New("btree: key already exists")
	ErrKeyNotFound  = errors.New("btree: key not found")
	ErrTreeFull     = errors.New("btree: key count would exceed maxKeys")
	ErrInvalidMax   = errors.New("btree: maxKeys must be >= 0")
)

// Tree is an in-process 2-3 tree; visited is unexported (section 4).
type Tree struct {
	root    *bnode.Node
	size    int
	maxKeys int
	visited atomic.Int64
	mu      sync.RWMutex
}

func New(maxKeys int) (*Tree, error) {
	if maxKeys < 0 {
		return nil, ErrInvalidMax
	}
	return &Tree{maxKeys: maxKeys}, nil
}
func (t *Tree) Size() int { t.mu.RLock(); defer t.mu.RUnlock(); return t.size }
func (t *Tree) Valid() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.root == nil && t.size == 0 || t.root != nil &&
		t.root.Validate() == nil && len(t.root.Inorder(nil)) == t.size
}

// Insert adds k and returns split cost (root cascade included). Rejected
// duplicate/full calls mutate nothing, not even visited.
func (t *Tree) Insert(k int) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.containsLocked(k) {
		return 0, ErrDuplicateKey
	}
	if t.size >= t.maxKeys {
		return 0, ErrTreeFull
	}
	vis, sp := 0, 0
	if t.root == nil {
		t.root, vis = &bnode.Node{Keys: []int{k}}, 1
	} else {
		l, m, r, split, err := insertRec(t.root, k, &vis, &sp)
		if err != nil {
			return 0, err // failure detected before any mutation
		}
		if split {
			t.root = &bnode.Node{Keys: []int{m}, Kids: []*bnode.Node{l, r}}
		}
	}
	t.size++
	t.visited.Store(int64(vis))
	return sp, nil
}

// Delete removes k and returns borrow+merge cost. Missing key: no mutation;
// internal key: replaced by its in-order predecessor before deletion.
func (t *Tree) Delete(k int) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.root == nil {
		return 0, ErrKeyNotFound
	}
	vis, repairs := 0, 0
	under, found := deleteRec(t.root, k, &vis, &repairs)
	if !found {
		return 0, ErrKeyNotFound
	}
	if under { // empty root: nil if leaf, else its sole child
		if r := t.root; r.IsLeaf() {
			t.root = nil
		} else {
			t.root = r.Kids[0]
		}
	}
	t.size--
	t.visited.Store(int64(vis))
	return repairs, nil
}

func (t *Tree) Search(k int) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	n := 0
	for cur := t.root; cur != nil; {
		n++
		i := cur.FindIndex(k)
		if i < len(cur.Keys) && cur.Keys[i] == k {
			t.visited.Store(int64(n))
			return true
		}
		if cur.IsLeaf() {
			break
		}
		cur = cur.Kids[i]
	}
	t.visited.Store(int64(n))
	return false
}
func (t *Tree) OrderedKeys() []int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.root == nil {
		return []int{}
	}
	return t.root.Inorder(nil)
}
func (t *Tree) Height() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	h, cur := 0, t.root
	for cur != nil {
		h++
		if cur.IsLeaf() {
			break
		}
		cur = cur.Kids[0]
	}
	return h
}

func (t *Tree) containsLocked(k int) bool {
	for cur := t.root; cur != nil; {
		i := cur.FindIndex(k)
		if i < len(cur.Keys) && cur.Keys[i] == k {
			return true
		}
		if cur.IsLeaf() {
			return false
		}
		cur = cur.Kids[i]
	}
	return false
}
