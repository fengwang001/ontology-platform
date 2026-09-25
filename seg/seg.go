// Package seg 提供线段树的底层存储与下标辅助，不依赖其他包。
package seg

import "math"

// Inf 是最小值单位元：任意 v 与 Inf 取 min 都得 v。
// 空区间查询与补齐叶子都用它，保证不污染结果。
const Inf = int64(math.MaxInt64)

// Tree 是一棵按数组堆式存储的线段树。
// 叶子从下标 Size 开始，共 Size 片；内部节点 1..Size-1；根为 1。
type Tree struct {
	Nodes []int64 // 长度 2*Size，下标 0 不用
	Size  int     // 叶子层宽度（>= n 的最小 2 的幂）
	N     int     // 真实元素个数
}

// NextPow2 返回 >= n 的最小 2 的幂，n<=1 时返回 1。
func NextPow2(n int) int {
	s := 1
	for s < n {
		s <<= 1
	}
	return s
}

// New 分配一棵宽度覆盖 n 的树，所有节点先填 Inf。
func New(n int) *Tree {
	s := NextPow2(n)
	nodes := make([]int64, 2*s)
	for i := range nodes {
		nodes[i] = Inf
	}
	return &Tree{Nodes: nodes, Size: s, N: n}
}

// Left 返回节点 i 的左子下标。
func Left(i int) int { return 2 * i }

// Right 返回节点 i 的右子下标。
func Right(i int) int { return 2*i + 1 }

// Parent 返回节点 i 的父节点下标。
func Parent(i int) int { return i / 2 }

// Leaf 返回数组下标 k 对应的叶子节点下标。
func (t *Tree) Leaf(k int) int { return t.Size + k }

// Min 返回 a、b 中较小者。
func Min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
