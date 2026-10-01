package ontology

import (
	"sort"
	"sync"
)

// Stats reports how many containers use each representation.
type Stats struct {
	ArrayContainers  int
	BitmapContainers int
}

// SparseBitset is a concurrency-safe adaptive sparse bitset of uint32 values.
type SparseBitset struct {
	mu sync.RWMutex
	// containers stores one *container per present high-16-bit key, ordered by key.
	keys       []uint16
	containers map[uint16]*container
}

// New returns an empty SparseBitset.
func New() *SparseBitset {
	return &SparseBitset{containers: make(map[uint16]*container)}
}

func keyOf(x uint32) uint16 { return uint16(x >> 16) }

func lowOf(x uint32) uint16 { return uint16(x) }

// insertKey keeps s.keys sorted when a new high key appears.
func (s *SparseBitset) insertKey(k uint16) {
	idx := sort.Search(len(s.keys), func(i int) bool { return s.keys[i] >= k })
	if idx < len(s.keys) && s.keys[idx] == k {
		return
	}
	s.keys = append(s.keys, 0)
	copy(s.keys[idx+1:], s.keys[idx:])
	s.keys[idx] = k
}

func (s *SparseBitset) Add(x uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := keyOf(x)
	c, ok := s.containers[k]
	if !ok {
		c = newArrayContainer(nil)
		s.containers[k] = c
		s.insertKey(k)
	}
	s.containers[k] = c.add(lowOf(x))
}

func (s *SparseBitset) Remove(x uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := keyOf(x)
	c, ok := s.containers[k]
	if !ok {
		return
	}
	c = c.remove(lowOf(x))
	if c.card == 0 {
		delete(s.containers, k)
		idx := sort.Search(len(s.keys), func(i int) bool { return s.keys[i] >= k })
		s.keys = append(s.keys[:idx], s.keys[idx+1:]...)
		return
	}
	s.containers[k] = c
}

func (s *SparseBitset) Contains(x uint32) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.containers[keyOf(x)]
	return ok && c.contains(lowOf(x))
}

func (s *SparseBitset) Cardinality() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var n uint64
	for _, k := range s.keys {
		n += uint64(s.containers[k].card)
	}
	return n
}

// Rank returns the number of elements in the set that are <= x.
func (s *SparseBitset) Rank(x uint32) uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	k := keyOf(x)
	var n uint64
	idx := sort.Search(len(s.keys), func(i int) bool { return s.keys[i] >= k })
	for i := 0; i < idx; i++ {
		n += uint64(s.containers[s.keys[i]].card)
	}
	if idx < len(s.keys) && s.keys[idx] == k {
		n += uint64(s.containers[k].rank(lowOf(x)))
	}
	return n
}

// Select returns the k-th (0-based) smallest element.
func (s *SparseBitset) Select(k uint64) (uint32, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	remaining := k
	for _, key := range s.keys {
		c := s.containers[key]
		if uint64(c.card) > remaining {
			return uint32(key)<<16 | uint32(c.selectNth(int(remaining))), nil
		}
		remaining -= uint64(c.card)
	}
	return 0, ErrSelectOutOfBounds
}

const maxUint32Range = uint64(1) << 32

// AddRange adds every uint32 value in the half-open interval [lo, hi).
// It is rejected without changing the set when lo > hi (reported first) or
// hi > 2^32. lo == hi is a legal no-op.
func (s *SparseBitset) AddRange(lo, hi uint64) error {
	if lo > hi {
		return ErrInvalidRange
	}
	if hi > maxUint32Range {
		return ErrRangeOutOfBounds
	}
	if lo == hi {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	startKey := uint32(lo >> 16)
	endKey := uint32(hi >> 16)
	endRem := uint32(hi & 0xffff)

	// Iterate each container key touched by [lo, hi). The start key begins at
	// lo's low part; intermediate keys are fully covered; the end key stops at
	// hi's low part (exclusive). When hi is exactly a key boundary the virtual
	// end key is skipped, so the preceding real key stays fully covered.
	for key := startKey; key <= endKey; key++ {
		lowLo := uint32(0)
		if key == startKey {
			lowLo = uint32(lo & 0xffff)
		}
		lowHi := uint32(1 << 16)
		if key == endKey {
			lowHi = endRem
		}
		if lowLo >= lowHi || key > 0xffff {
			continue
		}
		k := uint16(key)
		c, ok := s.containers[k]
		if !ok {
			c = newArrayContainer(nil)
			s.containers[k] = c
			s.insertKey(k)
		}
		s.containers[k] = c.addLowRange(lowLo, lowHi)
	}
	return nil
}

// And returns a new set containing the intersection of s and other. Every
// result container is renormalized by its result cardinality, and containers
// with an empty intersection are omitted.
func (s *SparseBitset) And(other *SparseBitset) *SparseBitset {
	result := New()
	s.mu.RLock()
	defer s.mu.RUnlock()
	other.mu.RLock()
	defer other.mu.RUnlock()

	i, j := 0, 0
	for i < len(s.keys) && j < len(other.keys) {
		switch {
		case s.keys[i] == other.keys[j]:
			if c := andContainer(s.containers[s.keys[i]], other.containers[other.keys[j]]); c != nil {
				result.keys = append(result.keys, s.keys[i])
				result.containers[s.keys[i]] = c
			}
			i++
			j++
		case s.keys[i] < other.keys[j]:
			i++
		default:
			j++
		}
	}
	return result
}

func (s *SparseBitset) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var st Stats
	for _, k := range s.keys {
		if s.containers[k].isBitmap() {
			st.BitmapContainers++
		} else {
			st.ArrayContainers++
		}
	}
	return st
}
