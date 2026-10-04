// Package seqset 是有序、不相交、自动合并相邻区间的整数区间集合，
// 由带平衡因子的 AVL 树实现，用于按区间而非逐序号存储遥测序号归宿。
package seqset

// node 是 AVL 树节点，代表一个半开区间 [l,r)。区间之间两两不相邻。
type node struct {
	l, r   int64 // 半开区间端点
	left   *node
	right  *node
	height int8
	sum    int64 // 子树覆盖整数总数
	segs   int   // 子树区间段数
}

func h(n *node) int8 {
	if n == nil {
		return 0
	}
	return n.height
}

func (n *node) pull() {
	n.height = 1 + h(n.left)
	if h(n.right) > n.height-1 {
		n.height = 1 + h(n.right)
	}
	n.sum = n.r - n.l
	n.segs = 1
	if n.left != nil {
		n.sum += n.left.sum
		n.segs += n.left.segs
	}
	if n.right != nil {
		n.sum += n.right.sum
		n.segs += n.right.segs
	}
}

func rotateRight(n *node) *node {
	x := n.left
	n.left = x.right
	x.right = n
	n.pull()
	x.pull()
	return x
}

func rotateLeft(n *node) *node {
	x := n.right
	n.right = x.left
	x.left = n
	n.pull()
	x.pull()
	return x
}

func balance(n *node) *node {
	n.pull()
	if h(n.left)-h(n.right) > 1 {
		if h(n.left.right) > h(n.left.left) {
			n.left = rotateLeft(n.left)
		}
		return rotateRight(n)
	}
	if h(n.right)-h(n.left) > 1 {
		if h(n.right.left) > h(n.right.right) {
			n.right = rotateRight(n.right)
		}
		return rotateLeft(n)
	}
	return n
}

// removeMax 取出子树最大节点，返回新树根与该节点（其左右子树已清空）。
func removeMax(n *node) (*node, *node) {
	if n.right == nil {
		x := n.left
		n.left = nil
		n.pull()
		return x, n
	}
	var x *node
	n.right, x = removeMax(n.right)
	return balance(n), x
}

// removeMin 取出子树最小节点，返回新树根与该节点。
func removeMin(n *node) (*node, *node) {
	if n.left == nil {
		x := n.right
		n.right = nil
		n.pull()
		return x, n
	}
	var x *node
	n.left, x = removeMin(n.left)
	return balance(n), x
}

// Set 是半开整数区间 [l,r) 的有序集合；相邻区间在插入时自动合并。
type Set struct {
	root    *node
	visited int
}

// New 返回空集合。
func New() *Set { return &Set{} }

// Contains 报告 x 是否属于集合。一次查找经过的节点数可由 Visited 读取。
func (s *Set) Contains(x int64) bool {
	_, _, ok := s.find(x)
	return ok
}

// Insert 把半开区间 [l,r) 并入集合，返回新区间实际新覆盖的整数个数。
func (s *Set) Insert(l, r int64) int64 {
	if r <= l {
		return 0
	}
	before := s.Count()
	s.root = s.insert(s.root, l, r)
	return s.Count() - before
}

// insert 把 [l,r) 并入以 n 为根的子树，吸收所有与之相交或相邻（半开相邻）的区间。
func (s *Set) insert(n *node, l, r int64) *node {
	if n == nil {
		z := &node{l: l, r: r}
		z.pull()
		return z
	}
	if r < n.l {
		n.left = s.insert(n.left, l, r)
		return balance(n)
	}
	if l > n.r {
		n.right = s.insert(n.right, l, r)
		return balance(n)
	}
	// 与 n 相交或半开相邻（r == n.l 或 l == n.r）：并入 n 并吸收邻居。
	if l > n.l {
		l = n.l
	}
	if r < n.r {
		r = n.r
	}
	var ok bool
	n, l, r, ok = s.absorb(n, l, r)
	_ = ok
	return n
}

// find 沿树查找覆盖 x 的节点，统计经过节点数。
func (s *Set) find(x int64) (int64, int64, bool) {
	s.visited = 0
	n := s.root
	for n != nil {
		s.visited++
		switch {
		case x < n.l:
			n = n.left
		case x >= n.r:
			n = n.right
		default:
			return n.l, n.r, true
		}
	}
	return 0, 0, false
}

// absorb 反复吸收与半开区间 [l,r) 相交或相邻的节点，返回新根与扩大后的端点。
func (s *Set) absorb(n *node, l, r int64) (*node, int64, int64, bool) {
	for {
		changed := false
		// 吸收左邻：左子树最大节点若其右端 >= l（cur.r == l 即半开相邻）。
		if n.left != nil {
			cur := n.left
			s.visited++
			for cur.right != nil {
				s.visited++
				cur = cur.right
			}
			if cur.r >= l {
				if cur.l < l {
					l = cur.l
				}
				n.left, _ = removeMax(n.left)
				changed = true
			}
		}
		// 吸收右邻：右子树最小节点若其左端 <= r（cur.l == r 即半开相邻）。
		if n.right != nil {
			cur := n.right
			s.visited++
			for cur.left != nil {
				s.visited++
				cur = cur.left
			}
			if cur.l <= r {
				if cur.r > r {
					r = cur.r
				}
				n.right, _ = removeMin(n.right)
				changed = true
			}
		}
		if !changed {
			n.l, n.r = l, r
			return balance(n), l, r, true
		}
	}
}

// Count 返回集合包含的整数总个数。
func (s *Set) Count() int64 {
	if s.root == nil {
		return 0
	}
	return s.root.sum
}

// Segments 返回区间段数。
func (s *Set) Segments() int {
	if s.root == nil {
		return 0
	}
	return s.root.segs
}

// First 返回最小成员序号；集合为空时 ok 为 false。
func (s *Set) First() (int64, bool) {
	n := s.root
	if n == nil {
		return 0, false
	}
	for n.left != nil {
		n = n.left
	}
	return n.l, true
}

// FirstBefore 返回集合中严格小于 x 的最大成员；不存在时 ok 为 false。
func (s *Set) FirstBefore(x int64) (int64, bool) {
	var best int64
	found := false
	n := s.root
	for n != nil {
		if n.l < x {
			if n.r <= x {
				best, found = n.r-1, true
				n = n.right
				continue
			}
			// x 落在该区间内部：最大严格小于 x 的成员即 x-1。
			return x - 1, true
		}
		n = n.left
	}
	return best, found
}

// Range 返回覆盖点 x 的半开区间 [l,r)；x 不属于集合时 ok 为 false。
func (s *Set) Range(x int64) (l, r int64, ok bool) { return s.find(x) }

// Visited 返回上一次 find 类操作（Contains/Range 等）经过的节点数。
func (s *Set) Visited() int { return s.visited }
