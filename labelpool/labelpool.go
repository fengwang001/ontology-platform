// Package labelpool manages a tunnel label interval: which labels are
// allocated, which are free, and which are held down in isolation after a
// release. Allocation always picks the smallest available label without
// scanning the interval.
package labelpool

import (
	"container/heap"
	"errors"
)

// ErrExhausted reports that no label is currently available.
var ErrExhausted = errors.New("labelpool: no available label")

type pheapItem struct {
	label int
	prio  uint64
}

// pheap is an indexed min-heap of labels ordered by (prio, label). The index
// allows O(log n) removal of arbitrary labels so no stale entries accumulate.
type pheap struct {
	items []pheapItem
	pos   map[int]int
}

func newPheap() *pheap {
	return &pheap{pos: make(map[int]int)}
}

func (h *pheap) Len() int { return len(h.items) }

func (h *pheap) Less(i, j int) bool {
	a, b := h.items[i], h.items[j]
	return a.prio < b.prio || (a.prio == b.prio && a.label < b.label)
}

func (h *pheap) Swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
	h.pos[h.items[i].label] = i
	h.pos[h.items[j].label] = j
}

func (h *pheap) Push(x any) {
	item := x.(pheapItem)
	h.pos[item.label] = len(h.items)
	h.items = append(h.items, item)
}

func (h *pheap) Pop() any {
	old := h.items
	n := len(old)
	item := old[n-1]
	h.items = old[:n-1]
	delete(h.pos, item.label)
	return item
}

func (h *pheap) push(label int, prio uint64) {
	heap.Push(h, pheapItem{label: label, prio: prio})
}

func (h *pheap) pop() pheapItem {
	return heap.Pop(h).(pheapItem)
}

func (h *pheap) remove(label int) {
	if i, ok := h.pos[label]; ok {
		heap.Remove(h, i)
	}
}

func (h *pheap) peek() (pheapItem, bool) {
	if len(h.items) == 0 {
		return pheapItem{}, false
	}
	return h.items[0], true
}

func (h *pheap) has(label int) bool {
	_, ok := h.pos[label]
	return ok
}

// Pool is a label interval [lo, hi] with a hold-down isolation period hd.
// It is not safe for concurrent use; callers must serialize access.
type Pool struct {
	lo, hi int
	hd     uint64
	next   int // smallest label never allocated; hi+1 when exhausted

	alloc    map[int]bool   // label -> currently bound
	isoUntil map[int]uint64 // label -> moment it becomes available again
	free     *pheap         // available labels, prio 0 (ordered by label)
	iso      *pheap         // isolated labels, prio = available-at

	// probes: labels examined and isolation expirations promoted during the
	// most recent AllocateMin call.
	examined   int
	promotions int
}

// NewPool returns a pool for labels [lo, hi] with hold-down hd milliseconds.
func NewPool(lo, hi int, hd uint64) *Pool {
	return &Pool{
		lo:       lo,
		hi:       hi,
		hd:       hd,
		next:     lo,
		alloc:    make(map[int]bool),
		isoUntil: make(map[int]uint64),
		free:     newPheap(),
		iso:      newPheap(),
	}
}

// AllocateMin returns the smallest label available at now. Labels whose
// isolation expired at or before now become available first. The second
// return value is false when every label is allocated or still isolated.
func (p *Pool) AllocateMin(now uint64) (int, bool) {
	p.examined, p.promotions = 0, 0
	for {
		top, ok := p.iso.peek()
		if !ok || top.prio > now {
			break
		}
		p.iso.pop()
		delete(p.isoUntil, top.label)
		p.free.push(top.label, 0)
		p.examined++
		p.promotions++
	}
	var label int
	if top, ok := p.free.peek(); ok {
		p.examined++
		if p.next <= p.hi && p.next < top.label {
			label = p.next
			p.next++
		} else {
			label = top.label
			p.free.pop()
		}
	} else if p.next <= p.hi {
		label = p.next
		p.next++
	} else {
		return 0, false
	}
	p.alloc[label] = true
	return label, true
}

// AllocateSpecific takes a particular label out of circulation, whether it is
// free, still isolated (affinity retake), or never allocated. It reports
// false when the label is out of range or already allocated.
func (p *Pool) AllocateSpecific(label int) bool {
	if label < p.lo || label > p.hi || p.alloc[label] {
		return false
	}
	if _, ok := p.isoUntil[label]; ok {
		delete(p.isoUntil, label)
		p.iso.remove(label)
	} else if label >= p.next {
		for p.next < label {
			p.free.push(p.next, 0)
			p.next++
		}
		p.next++
	} else {
		if !p.free.has(label) {
			return false
		}
		p.free.remove(label)
	}
	p.alloc[label] = true
	return true
}

// Free releases label at the given moment; it stays isolated until at+hd.
func (p *Pool) Free(label int, at uint64) {
	if !p.alloc[label] {
		return
	}
	delete(p.alloc, label)
	p.isoUntil[label] = at + p.hd
	p.iso.push(label, at+p.hd)
}

// WouldAvailable reports whether any label would be available at now,
// without mutating the pool.
func (p *Pool) WouldAvailable(now uint64) bool {
	if p.next <= p.hi || p.free.Len() > 0 {
		return true
	}
	top, ok := p.iso.peek()
	return ok && top.prio <= now
}

// Examined returns how many candidate labels the last AllocateMin inspected.
func (p *Pool) Examined() int { return p.examined }

// Promotions returns how many isolated labels became available during the
// last AllocateMin call.
func (p *Pool) Promotions() int { return p.promotions }

// IsAllocated reports whether label is currently bound.
func (p *Pool) IsAllocated(label int) bool { return p.alloc[label] }

// IsolatedUntil returns the moment label becomes available again, if it is
// currently isolated.
func (p *Pool) IsolatedUntil(label int) (uint64, bool) {
	t, ok := p.isoUntil[label]
	return t, ok
}

// Allocated returns how many labels are currently bound.
func (p *Pool) Allocated() int { return len(p.alloc) }
