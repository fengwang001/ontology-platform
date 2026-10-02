package rtree

import "sync"

// Rect is a closed rectangle [X1,X2] x [Y1,Y2] with integer coordinates.
type Rect struct {
	X1, Y1, X2, Y2 int64
}

// leaf entry: a stored object. child == nil.
// internal entry: a subtree rooted at child; ID == 0.
type entry struct {
	id    int64
	rect  Rect
	child *node
}

type node struct {
	height  int // leaves have height 0
	entries []entry
}

// RTree is a concurrent-safe 2D integer R-tree with
// conditional-shrink reinsertion on deletion.
type RTree struct {
	mu      sync.RWMutex
	m, M, C int
	count   int
	root    *node
	objects map[int64]Rect
	visited int
	located int
}

// New validates parameters and creates an empty tree.
func New(M, m, C int) (*RTree, error) {
	if M < 3 || M > 16 || m < 1 || m > M/2 || C < 1 || C > 1_000_000 {
		return nil, ErrInvalid
	}
	return &RTree{
		m:       m,
		M:       M,
		C:       C,
		root:    &node{height: 0},
		objects: make(map[int64]Rect),
	}, nil
}

// InsertResult reports a successful insertion.
type InsertResult struct {
	Splits int
}

// Insert adds an object and returns the number of node splits performed.
func (t *RTree) Insert(id int64, rect Rect) (InsertResult, error) {
	if id <= 0 || !validateRect(rect) {
		return InsertResult{}, ErrInvalid
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.objects[id]; exists {
		return InsertResult{}, ErrDuplicate
	}
	if t.count >= t.C {
		return InsertResult{}, ErrFull
	}
	t.count++
	t.objects[id] = rect
	splits := t.insertAtLevel(entry{id: id, rect: rect}, 0)
	return InsertResult{Splits: splits}, nil
}

// DeleteResult reports a successful deletion.
type DeleteResult struct {
	Removed    int // nodes detached
	Reinserted int // entries reinserted
}

// Delete removes an object by id.
func (t *RTree) Delete(id int64) (DeleteResult, error) {
	if id <= 0 {
		return DeleteResult{}, ErrInvalid
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.objects[id]; !exists {
		return DeleteResult{}, ErrNotFound
	}
	return t.deleteLocked(id), nil
}

// Search returns ids of objects intersecting rect, ascending.
func (t *RTree) Search(rect Rect) ([]int64, error) {
	if !validateRect(rect) {
		return nil, ErrInvalid
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.searchLocked(rect), nil
}

// Visited returns the number of nodes entered by the last Search.
func (t *RTree) Visited() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.visited
}

// Located returns the number of nodes entered during the locating
// phase of the last Delete.
func (t *RTree) Located() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.located
}

// Dump renders the tree in pre-order.
func (t *RTree) Dump() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.dumpLocked()
}

// Count returns the number of stored objects.
func (t *RTree) Count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.count
}
