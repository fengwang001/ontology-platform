// Package reclaim decides which slot indexes are reused and in which order.
package reclaim

// Allocator hands out slot indexes under a fixed capacity and reclaims
// released indexes in a deterministic order (lowest free index first).
type Allocator struct {
	capacity int
	next     uint32
	free     minHeap
	inUse    int
}

// New creates an allocator limited to capacity indexes.
func New(capacity int) *Allocator {
	return &Allocator{capacity: capacity}
}

// Acquire returns the lowest currently available index.
func (a *Allocator) Acquire() (uint32, bool) {
	if a.inUse >= a.capacity {
		return 0, false
	}
	a.inUse++
	if n := len(a.free); n > 0 {
		idx := a.free.PopIndex()
		return idx, true
	}
	idx := a.next
	a.next++
	return idx, true
}

// Release makes an index available again.
func (a *Allocator) Release(index uint32) {
	a.free.PushIndex(index)
	a.inUse--
}

// InUse reports how many indexes are currently handed out.
func (a *Allocator) InUse() int { return a.inUse }
