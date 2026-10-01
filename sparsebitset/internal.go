package sparsebitset

import (
	"sort"
	"unsafe"
)

func uintptrOf(s *Set) uintptr {
	return uintptr(unsafe.Pointer(s))
}

// findKey returns the index of the container with the given key, or the
// insertion position and false when absent.
func (s *Set) findKey(key uint16) (int, bool) {
	i := sort.Search(len(s.keys), func(i int) bool { return s.keys[i] >= key })
	if i < len(s.keys) && s.keys[i] == key {
		return i, true
	}
	return i, false
}

func (s *Set) add(x uint32) {
	key := uint16(x >> 16)
	i, found := s.findKey(key)
	if !found {
		c := &container{array: []uint16{uint16(x)}, card: 1}
		s.keys = append(s.keys, 0)
		copy(s.keys[i+1:], s.keys[i:])
		s.keys[i] = key
		s.containers = append(s.containers, nil)
		copy(s.containers[i+1:], s.containers[i:])
		s.containers[i] = c
		return
	}
	s.containers[i].add(uint16(x))
}

func (s *Set) remove(x uint32) {
	key := uint16(x >> 16)
	i, found := s.findKey(key)
	if !found {
		return
	}
	c := s.containers[i]
	c.remove(uint16(x))
	if c.card == 0 {
		s.keys = append(s.keys[:i], s.keys[i+1:]...)
		s.containers = append(s.containers[:i], s.containers[i+1:]...)
	}
}

func (s *Set) contains(x uint32) bool {
	i, found := s.findKey(uint16(x >> 16))
	return found && s.containers[i].contains(uint16(x))
}

func (s *Set) cardinality() int {
	n := 0
	for _, c := range s.containers {
		n += c.card
	}
	return n
}

func (s *Set) and(other *Set) *Set {
	result := New()
	i, j := 0, 0
	for i < len(s.keys) && j < len(other.keys) {
		switch {
		case s.keys[i] < other.keys[j]:
			i++
		case s.keys[i] > other.keys[j]:
			j++
		default:
			if c := andContainers(s.containers[i], other.containers[j]); c != nil {
				result.keys = append(result.keys, s.keys[i])
				result.containers = append(result.containers, c)
			}
			i++
			j++
		}
	}
	return result
}

func (s *Set) rank(x uint32) int {
	key := uint16(x >> 16)
	n := 0
	for i, k := range s.keys {
		if k > key {
			break
		}
		if k == key {
			n += s.containers[i].rank(uint16(x))
			break
		}
		n += s.containers[i].card
	}
	return n
}

func (s *Set) selectK(k int) (uint32, error) {
	if k < 0 {
		return 0, ErrSelectOutOfRange
	}
	for i, c := range s.containers {
		if k < c.card {
			return uint32(s.keys[i])<<16 | uint32(c.selectK(k)), nil
		}
		k -= c.card
	}
	return 0, ErrSelectOutOfRange
}

// addRange inserts [lo, hi); callers guarantee lo < hi <= 2^32.
func (s *Set) addRange(lo, hi uint64) {
	loKey := uint32(lo >> 16)
	hiKey := uint32((hi - 1) >> 16)
	for key := loKey; key <= hiKey; key++ {
		cLo := uint32(0)
		if key == loKey {
			cLo = uint32(lo & 0xffff)
		}
		cHi := uint32(1) << 16
		if key == hiKey {
			cHi = uint32((hi-1)&0xffff) + 1
		}
		i, found := s.findKey(uint16(key))
		if !found {
			c := &container{array: make([]uint16, 0, 1)}
			s.keys = append(s.keys, 0)
			copy(s.keys[i+1:], s.keys[i:])
			s.keys[i] = uint16(key)
			s.containers = append(s.containers, nil)
			copy(s.containers[i+1:], s.containers[i:])
			s.containers[i] = c
		}
		s.containers[i].addRange(cLo, cHi)
	}
}
