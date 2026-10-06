package parking

import "container/heap"

type minIDSet struct {
	shift0  map[uint64]uint64
	shift6  map[uint64]uint64
	shift12 map[uint64]uint64
	shift18 map[uint64]uint64
	shift24 map[uint64]uint64
	shift30 uint64
}

func newMinIDSet() *minIDSet {
	return &minIDSet{
		shift0:  make(map[uint64]uint64),
		shift6:  make(map[uint64]uint64),
		shift12: make(map[uint64]uint64),
		shift18: make(map[uint64]uint64),
		shift24: make(map[uint64]uint64),
	}
}

func lowBit(v uint64) int {
	if v == 0 {
		return 64
	}
	n := 0
	for v&1 == 0 {
		v >>= 1
		n++
	}
	return n
}

func (s *minIDSet) Add(id int) {
	x := uint64(id)
	s.shift0[x>>6] |= 1 << (x & 63)
	s.shift6[x>>12] |= 1 << ((x >> 6) & 63)
	s.shift12[x>>18] |= 1 << ((x >> 12) & 63)
	s.shift18[x>>24] |= 1 << ((x >> 18) & 63)
	s.shift24[x>>30] |= 1 << ((x >> 24) & 63)
	s.shift30 |= 1 << ((x >> 30) & 63)
}

func (s *minIDSet) Remove(id int) {
	x := uint64(id)
	s.shift0[x>>6] &^= 1 << (x & 63)
	if value := s.shift0[x>>6]; value != 0 {
		return
	}
	delete(s.shift0, x>>6)
	s.shift6[x>>12] &^= 1 << ((x >> 6) & 63)
	if s.shift6[x>>12] != 0 {
		return
	}
	delete(s.shift6, x>>12)
	s.shift12[x>>18] &^= 1 << ((x >> 12) & 63)
	if s.shift12[x>>18] != 0 {
		return
	}
	delete(s.shift12, x>>18)
	s.shift18[x>>24] &^= 1 << ((x >> 18) & 63)
	if s.shift18[x>>24] != 0 {
		return
	}
	delete(s.shift18, x>>24)
	s.shift24[x>>30] &^= 1 << ((x >> 24) & 63)
	if s.shift24[x>>30] != 0 {
		return
	}
	delete(s.shift24, x>>30)
	s.shift30 &^= 1 << (x >> 30)
}

func (s *minIDSet) Min() (int, bool) {
	if s.shift30 == 0 {
		return 0, false
	}
	top := uint64(lowBit(s.shift30))
	b24, ok := s.shift24[top]
	if !ok || b24 == 0 {
		return 0, false
	}
	x := top<<6 | uint64(lowBit(b24))
	b18, ok := s.shift18[x]
	if !ok || b18 == 0 {
		return 0, false
	}
	x = x<<6 | uint64(lowBit(b18))
	b12, ok := s.shift12[x]
	if !ok || b12 == 0 {
		return 0, false
	}
	x = x<<6 | uint64(lowBit(b12))
	b6, ok := s.shift6[x]
	if !ok || b6 == 0 {
		return 0, false
	}
	x = x<<6 | uint64(lowBit(b6))
	b0, ok := s.shift0[x]
	if !ok || b0 == 0 {
		return 0, false
	}
	x = x<<6 | uint64(lowBit(b0))
	return int(x), true
}

type eventHeap []scheduledEvent

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].at != h[j].at {
		return h[i].at < h[j].at
	}
	return h[i].kind < h[j].kind
}
func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x any)   { *h = append(*h, x.(scheduledEvent)) }
func (h *eventHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

func (h *eventHeap) push(e scheduledEvent) { heap.Push(h, e) }
func (h *eventHeap) pop() scheduledEvent   { return heap.Pop(h).(scheduledEvent) }
