package rtree

import (
	"errors"
	"sync"
	"sync/atomic"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrDuplicateID     = errors.New("duplicate id")
	ErrObjectNotFound  = errors.New("object not found")
	ErrCapacityFull    = errors.New("capacity full")
)

type Rect struct {
	X1, Y1, X2, Y2 int64
}

type RTree struct {
	mu      sync.RWMutex
	max     int
	min     int
	cap     int
	objects map[int64]Rect
	root    *node
	visited atomic.Int64
	located atomic.Int64
}

func New(M int, m int, capacity int) (*RTree, error) {
	if M < 3 || M > 16 || m < 1 || m > M/2 || capacity < 1 || capacity > 1_000_000 {
		return nil, ErrInvalidArgument
	}
	tree := &RTree{
		max:     M,
		min:     m,
		cap:     capacity,
		objects: make(map[int64]Rect),
		root:    &node{},
	}
	return tree, nil
}

func (t *RTree) Insert(id int64, rect Rect) (int, error) {
	if id <= 0 || !validRect(rect) {
		return 0, ErrInvalidArgument
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if _, exists := t.objects[id]; exists {
		return 0, ErrDuplicateID
	}
	if len(t.objects) >= t.cap {
		return 0, ErrCapacityFull
	}

	splits := t.insertLocked(entry{id: id, rect: rect, height: 0})
	t.objects[id] = rect
	return splits, nil
}

func (t *RTree) Delete(id int64) (int, int, error) {
	if id <= 0 {
		return 0, 0, ErrInvalidArgument
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	rect, exists := t.objects[id]
	if !exists {
		return 0, 0, ErrObjectNotFound
	}

	removed, reinserted, _, located, found := t.deleteLocked(id, rect)
	if !found {
		return 0, 0, ErrObjectNotFound
	}
	t.located.Store(int64(located))
	delete(t.objects, id)
	return removed, reinserted, nil
}
