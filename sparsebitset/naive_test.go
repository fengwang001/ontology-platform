package sparsebitset

import "sort"

// naiveSet 是用有序切片实现的朴素集合，作为对拍参照模型。
type naiveSet struct {
	xs []uint32 // 升序、去重
}

func (n *naiveSet) search(x uint32) (int, bool) {
	i := sort.Search(len(n.xs), func(i int) bool { return n.xs[i] >= x })
	return i, i < len(n.xs) && n.xs[i] == x
}

func (n *naiveSet) Add(x uint32) {
	i, found := n.search(x)
	if found {
		return
	}
	n.xs = append(n.xs, 0)
	copy(n.xs[i+1:], n.xs[i:])
	n.xs[i] = x
}

func (n *naiveSet) Remove(x uint32) {
	i, found := n.search(x)
	if !found {
		return
	}
	copy(n.xs[i:], n.xs[i+1:])
	n.xs = n.xs[:len(n.xs)-1]
}

func (n *naiveSet) AddRange(lo, hi uint64) {
	for x := lo; x < hi; x++ {
		n.Add(uint32(x))
	}
}

func (n *naiveSet) And(o *naiveSet) *naiveSet {
	out := &naiveSet{}
	i, j := 0, 0
	for i < len(n.xs) && j < len(o.xs) {
		switch {
		case n.xs[i] < o.xs[j]:
			i++
		case n.xs[i] > o.xs[j]:
			j++
		default:
			out.xs = append(out.xs, n.xs[i])
			i++
			j++
		}
	}
	return out
}

func (n *naiveSet) Rank(x uint32) int {
	i := sort.Search(len(n.xs), func(i int) bool { return n.xs[i] > x })
	return i
}

func (n *naiveSet) Select(k int) (uint32, bool) {
	if k < 0 || k >= len(n.xs) {
		return 0, false
	}
	return n.xs[k], true
}

func (n *naiveSet) Cardinality() int {
	return len(n.xs)
}

// Stats 按“表示类型是元素集合的纯函数”这一规范，
// 由每个高 16 位容器内的元素个数推出期望的容器表示。
func (n *naiveSet) Stats() (arrays, bitmaps int) {
	counts := map[uint16]int{}
	for _, x := range n.xs {
		counts[uint16(x>>16)]++
	}
	for _, c := range counts {
		if c > arrayThreshold {
			bitmaps++
		} else {
			arrays++
		}
	}
	return arrays, bitmaps
}
