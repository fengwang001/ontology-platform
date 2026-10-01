package sparsebitset

import "sort"

// naiveSet is a reference implementation backed by a sorted slice, used
// to differential-test the adaptive container set.
type naiveSet struct {
	elems []uint32
}

func (n *naiveSet) add(x uint32) {
	i := sort.Search(len(n.elems), func(i int) bool { return n.elems[i] >= x })
	if i < len(n.elems) && n.elems[i] == x {
		return
	}
	n.elems = append(n.elems, 0)
	copy(n.elems[i+1:], n.elems[i:])
	n.elems[i] = x
}

func (n *naiveSet) remove(x uint32) {
	i := sort.Search(len(n.elems), func(i int) bool { return n.elems[i] >= x })
	if i < len(n.elems) && n.elems[i] == x {
		n.elems = append(n.elems[:i], n.elems[i+1:]...)
	}
}

func (n *naiveSet) addRange(lo, hi uint64) {
	for v := lo; v < hi; v++ {
		n.add(uint32(v))
	}
}

func (n *naiveSet) rank(x uint32) int {
	return sort.Search(len(n.elems), func(i int) bool { return n.elems[i] > x })
}

func (n *naiveSet) selectK(k int) (uint32, bool) {
	if k < 0 || k >= len(n.elems) {
		return 0, false
	}
	return n.elems[k], true
}

func (n *naiveSet) and(other *naiveSet) *naiveSet {
	result := &naiveSet{}
	i, j := 0, 0
	for i < len(n.elems) && j < len(other.elems) {
		switch {
		case n.elems[i] < other.elems[j]:
			i++
		case n.elems[i] > other.elems[j]:
			j++
		default:
			result.elems = append(result.elems, n.elems[i])
			i++
			j++
		}
	}
	return result
}

// expectedStats derives the required container split from the naive set:
// one container per distinct high-16-bit key, array iff its count <= 4096.
func (n *naiveSet) expectedStats() (arrays, bitmaps int) {
	counts := map[uint16]int{}
	for _, v := range n.elems {
		counts[uint16(v>>16)]++
	}
	for _, c := range counts {
		if c <= arrayMaxCardinality {
			arrays++
		} else {
			bitmaps++
		}
	}
	return arrays, bitmaps
}
