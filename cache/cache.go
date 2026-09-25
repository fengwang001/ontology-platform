// Package cache implements the slab cache: three-state slab sets, Alloc/Free, sentinel errors, complexity counter.
package cache

import (
	"container/heap"
	"errors"
	"sync"

	"ontology/slab"
)

var (
	ErrRawSize = errors.New("cache: raw size must be positive")
	ErrAlign   = errors.New("cache: align must be a power of two")
	ErrTooBig  = errors.New("cache: aligned size exceeds slab size")
	ErrBadFree = errors.New("cache: offset is not a live allocation")
)

const stEmpty, stPartial, stFull = 0, 1, 2

type slabInfo struct {
	free        slab.IntHeap // free slot indices inside this slab
	used, state int
}

// Cache is a slab allocator for one fixed object size.
type Cache struct {
	mu                    sync.Mutex
	sz, slabSize, perSlab int
	slabs                 []*slabInfo
	partial, empty        slab.IntHeap // lazy min-heaps of slab indices, by state
	allocated             map[int]struct{}
	checked               int // slabs inspected by the last Alloc/Free (unexported)
}

func New(rawSize, align, slabSize int) (*Cache, error) {
	sz := slab.AlignUp(rawSize, align)
	if rawSize <= 0 {
		return nil, ErrRawSize
	}
	if !slab.IsPow2(align) {
		return nil, ErrAlign
	}
	if sz > slabSize {
		return nil, ErrTooBig
	}
	return &Cache{sz: sz, slabSize: slabSize, perSlab: slab.PerSlab(slabSize, sz),
		allocated: make(map[int]struct{})}, nil
}

// top returns the smallest-index slab in h with state want (lazy deletion).
func (c *Cache) top(h *slab.IntHeap, want int) (int, bool) {
	for len(*h) > 0 {
		c.checked++
		if c.slabs[(*h)[0]].state == want {
			return (*h)[0], true
		}
		heap.Pop(h)
	}
	return -1, false
}

// Alloc returns the byte offset of a freshly allocated object.
func (c *Cache) Alloc() (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checked = 0
	if j, ok := c.top(&c.partial, stPartial); ok { // partial, smallest index
		s := c.slabs[j]
		off := c.take(j, heap.Pop(&s.free).(int))
		if s.state == stFull {
			heap.Pop(&c.partial)
		}
		return off, nil
	}
	j, ok := c.top(&c.empty, stEmpty) // empty slab, slot 0
	if ok {
		heap.Pop(&c.empty)
	} else { // grow by one slab, slot 0
		j = len(c.slabs)
		s := &slabInfo{free: make(slab.IntHeap, c.perSlab)}
		for i := range s.free {
			s.free[i] = i
		}
		c.slabs = append(c.slabs, s)
		c.checked++
	}
	return c.take(j, heap.Pop(&c.slabs[j].free).(int)), nil
}

// take records slot i of slab j as allocated and fixes j's state. Callers hold the lock.
func (c *Cache) take(j, i int) int {
	s := c.slabs[j]
	s.used++
	switch {
	case s.used == c.perSlab:
		s.state = stFull
	case s.state == stEmpty:
		s.state = stPartial
		heap.Push(&c.partial, j)
	}
	off := slab.Offset(j, i, c.slabSize, c.sz)
	c.allocated[off] = struct{}{}
	return off
}

// Free returns the object at off. off must be a live allocation.
func (c *Cache) Free(off int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checked = 0
	if _, ok := c.allocated[off]; !ok {
		return ErrBadFree
	}
	j, i := slab.Locate(off, c.slabSize, c.sz) // direct offset->slab, no scan
	c.checked = 1
	s := c.slabs[j]
	delete(c.allocated, off)
	heap.Push(&s.free, i)
	s.used--
	switch {
	case s.state == stFull:
		s.state = stPartial
		heap.Push(&c.partial, j)
	case s.state == stPartial && s.used == 0:
		s.state = stEmpty
		heap.Push(&c.empty, j)
	}
	return nil
}

// Stats is a consistent snapshot of the cache.
type Stats struct{ Slabs, Allocated, Free, Full, Partial, Empty int }

func (c *Cache) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := Stats{Slabs: len(c.slabs), Allocated: len(c.allocated)}
	for _, s := range c.slabs {
		st.Free += len(s.free)
		if s.state == stFull {
			st.Full++
		} else if s.state == stPartial {
			st.Partial++
		} else {
			st.Empty++
		}
	}
	return st
}
