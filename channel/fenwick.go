package channel

// fenwick 是前缀和树状数组，支持单点加与前缀和，均为 O(log n)。
// 容量按需倍增；由于树节点聚合其覆盖区间，增量扩容后旧值需要重建，
// 为此保留原始值数组 vals，扩容时整体重建。倍增扩容使重建总成本为 O(N)
// （几何级数），故每次 add 摊销 O(1)，查询始终 O(log N)。
type fenwick struct {
	vals []int64
	tree []int64
}

func newFenwick() *fenwick {
	return &fenwick{
		vals: make([]int64, 1),
		tree: make([]int64, 1),
	}
}

// ensure 使数组可容纳索引 idx；容量倍增后按原始值重建整棵树。
func (f *fenwick) ensure(idx int64) {
	if idx < int64(len(f.vals)) {
		return
	}
	size := int64(len(f.vals))
	for size <= idx {
		if size == 0 {
			size = 1
		} else {
			size *= 2
		}
	}
	grownVals := make([]int64, size)
	copy(grownVals, f.vals)
	f.vals = grownVals
	grownTree := make([]int64, size)
	for i := int64(1); i < size; i++ {
		grownTree[i] += grownVals[i]
		j := i + (i & -i)
		if j < size {
			grownTree[j] += grownTree[i]
		}
	}
	f.tree = grownTree
}

// add 给位置 idx（1-based）加上 delta。
func (f *fenwick) add(idx int64, delta int64) {
	if idx <= 0 {
		return
	}
	f.ensure(idx)
	f.vals[idx] += delta
	for i := idx; i < int64(len(f.tree)); i += i & -i {
		f.tree[i] += delta
	}
}

// prefixSum 返回 [1, idx] 的和；idx<=0 时为 0。
func (f *fenwick) prefixSum(idx int64) int64 {
	if idx <= 0 {
		return 0
	}
	if idx >= int64(len(f.tree)) {
		idx = int64(len(f.tree)) - 1
	}
	var sum int64
	for i := idx; i > 0; i -= i & -i {
		sum += f.tree[i]
	}
	return sum
}

// rangeSum 返回闭区间 [l,r] 上的和；l>r 时为 0。
func (f *fenwick) rangeSum(l, r int64) int64 {
	if l > r || r <= 0 {
		return 0
	}
	if l <= 0 {
		l = 1
	}
	return f.prefixSum(r) - f.prefixSum(l-1)
}
