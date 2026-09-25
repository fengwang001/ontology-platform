// Package cache implements the three-state slab cache and its bookkeeping.
package cache

import (
	"container/heap"
	"errors"
	"sync"

	"ontology/slab"
)

var (
	ErrBadSize     = errors.New("cache: rawSize must be positive")
	ErrBadAlign    = errors.New("cache: align must be a positive power of two")
	ErrDoesNotFit  = errors.New("cache: aligned object size exceeds slab size")
	ErrInvalidFree = errors.New("cache: free offset is not a live allocation")
)

type slabData struct {
	occ  []bool
	used int
}

// slabSet: membership map + lazy min-heap; smallest member found with no scan.
type slabSet struct {
	h  []int
	on map[int]bool
}

func newSet() slabSet                { return slabSet{on: map[int]bool{}} }
func (s slabSet) Len() int           { return len(s.h) }
func (s slabSet) Less(a, b int) bool { return s.h[a] < s.h[b] }
func (s slabSet) Swap(a, b int)      { s.h[a], s.h[b] = s.h[b], s.h[a] }
func (s *slabSet) Push(x any)        { s.h = append(s.h, x.(int)) }
func (s *slabSet) Pop() any          { x := s.h[len(s.h)-1]; s.h = s.h[:len(s.h)-1]; return x }
func (s *slabSet) add(j int) {
	if !s.on[j] {
		s.on[j] = true
		heap.Push(s, j)
	}
}
func (s *slabSet) drop(j int) { delete(s.on, j) }

// peek returns the smallest live member, lazily dropping stale heads.
func (s *slabSet) peek() (int, bool) {
	for len(s.h) > 0 && !s.on[s.h[0]] {
		heap.Pop(s)
	}
	if len(s.h) == 0 {
		return 0, false
	}
	return s.h[0], true
}

// Cache is concurrency-safe; checks is a non-exported slab-inspection counter.
type Cache struct {
	mu      sync.RWMutex
	geom    slab.Geometry
	slabs   []*slabData
	partial slabSet
	empty   slabSet
	live    map[int]struct{}
	checks  int
}

// NewCache validates every argument before allocating any state.
func NewCache(rawSize, align, slabSize int) (*Cache, error) {
	if rawSize <= 0 {
		return nil, ErrBadSize
	}
	if !slab.IsPowerOfTwo(align) {
		return nil, ErrBadAlign
	}
	g := slab.Pack(rawSize, align, slabSize)
	if g.Size > g.SlabSize {
		return nil, ErrDoesNotFit
	}
	return &Cache{geom: g, partial: newSet(), empty: newSet(), live: map[int]struct{}{}}, nil
}

// minFree marks and returns the smallest free slot of the chosen slab.
func minFree(s *slabData) int {
	for i := range s.occ {
		if !s.occ[i] {
			s.occ[i] = true
			return i
		}
	}
	panic("slab has no free slot")
}

// Alloc returns the lexicographically smallest free (slab, slot) offset:
// smallest partial slab, else a smaller empty slab's slot 0, else a new slab.
func (c *Cache) Alloc() (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checks = 0
	jp, pok := c.partial.peek()
	je, eok := c.empty.peek()
	if pok {
		c.checks++
	}
	var j, slot int
	switch {
	case pok && (!eok || jp < je):
		j, slot = jp, minFree(c.slabs[jp])
	case eok:
		c.checks++
		j = je
		c.empty.drop(je)
		c.slabs[je].occ[0] = true
	default:
		j = len(c.slabs)
		c.slabs = append(c.slabs, &slabData{occ: make([]bool, c.geom.PerSlab)})
		slot = minFree(c.slabs[j])
	}
	s := c.slabs[j]
	s.used++
	off := c.geom.Offset(j, slot)
	c.live[off] = struct{}{}
	if s.used == c.geom.PerSlab {
		c.partial.drop(j)
	} else if s.used == 1 {
		c.partial.add(j)
	}
	return off, nil
}

// Free maps off straight to its slab (no scan); bad/duplicate Free is a no-op.
func (c *Cache) Free(off int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.live[off]; !ok {
		return ErrInvalidFree
	}
	c.checks = 1
	j, i := c.geom.Split(off)
	s := c.slabs[j]
	delete(c.live, off)
	s.occ[i] = false
	wasFull := s.used == c.geom.PerSlab
	s.used--
	if s.used == 0 {
		c.partial.drop(j)
		c.empty.add(j)
	} else if wasFull {
		c.partial.add(j)
	}
	return nil
}
