// Package rank 维护单个 Key 的存活行有序集合，按排序键 (T, ID) 升序。
package rank

import "math/bits"
import "strconv"

type Row struct {
	ID string
	T  int64
}
type node struct {
	row         Row
	h           int
	left, right *node
}

// Set 是按 (T, ID) 排序的 AVL 存活行集合；非导出字段 cmp 记录最近一次
// Insert/Delete 的排序键比较次数，不进入任何公开接口。
type Set struct {
	root *node
	byID map[string]int64
	cmp  int
}

func New() *Set                   { return &Set{byID: map[string]int64{}} }
func (s *Set) Len() int           { return len(s.byID) }
func (s *Set) less(a, b Row) bool { s.cmp++; return a.T < b.T || a.T == b.T && a.ID < b.ID }
func (s *Set) Insert(r Row)       { s.cmp = 0; s.byID[r.ID] = r.T; s.root = s.insert(s.root, r) }

func (s *Set) insert(n *node, r Row) *node {
	if n == nil {
		return &node{row: r, h: 1}
	}
	if s.less(r, n.row) {
		n.left = s.insert(n.left, r)
	} else {
		n.right = s.insert(n.right, r)
	}
	return s.balance(n)
}

// Delete 按 ID 删除一行，返回该行是否存在；不存在时不改状态。
func (s *Set) Delete(id string) bool {
	t, ok := s.byID[id]
	if !ok {
		s.cmp = 0
		return false
	}
	s.cmp = 0
	delete(s.byID, id)
	s.root = s.del(s.root, Row{ID: id, T: t})
	return true
}

func (s *Set) del(n *node, r Row) *node {
	if n == nil {
		return nil
	}
	switch {
	case s.less(r, n.row):
		n.left = s.del(n.left, r)
	case s.less(n.row, r):
		n.right = s.del(n.right, r)
	case n.left == nil:
		return n.right
	case n.right == nil:
		return n.left
	default: // 后继为右子树最左下节点；沿指针下行不做排序键比较
		suc := n.right
		for suc.left != nil {
			suc = suc.left
		}
		n.row = suc.row
		n.right = s.del(n.right, suc.row)
	}
	return s.balance(n)
}

func (s *Set) Min() (Row, bool) {
	if s.root == nil {
		return Row{}, false
	}
	n := s.root
	for n.left != nil {
		n = n.left
	}
	return n.row, true
}

func height(n *node) int {
	if n == nil {
		return 0
	}
	return n.h
}

// balance 只依据子树高度差旋转，不产生排序键比较。
func (s *Set) balance(n *node) *node {
	if height(n.left)-height(n.right) > 1 {
		if height(n.left.right) > height(n.left.left) {
			n.left = s.rotate(n.left, true)
		}
		return s.rotate(n, false)
	}
	if height(n.right)-height(n.left) > 1 {
		if height(n.right.left) > height(n.right.right) {
			n.right = s.rotate(n.right, false)
		}
		return s.rotate(n, true)
	}
	n.h = 1 + max(height(n.left), height(n.right))
	return n
}

func (s *Set) rotate(n *node, left bool) *node {
	c := n.right
	if !left {
		c = n.left
	}
	if left {
		n.right = c.left
		c.left = n
	} else {
		n.left = c.right
		c.right = n
	}
	n.h = 1 + max(height(n.left), height(n.right))
	c.h = 1 + max(height(c.left), height(c.right))
	return c
}

// ComplexityCheck 供演示判定：多档 m 下两步比较次数均 ≤ 4*(⌊log2 m⌋+2)，只给布尔值。
func ComplexityCheck() bool {
	for _, m := range []int{100, 1000, 10000} {
		s, x := New(), uint64(20260924)
		for i := 0; i < m; i++ {
			x = x*6364136223846793005 + 1442695040888963407
			s.Insert(Row{ID: "id" + strconv.Itoa(i), T: int64(x % (1 << 40))})
		}
		mn, _ := s.Min()
		bound := 4 * (bits.Len(uint(m)) + 1)
		s.Insert(Row{ID: "zzz", T: 1 << 62})
		ci := s.cmp // 插入计数先存下，Delete 会清零重计
		s.Delete(mn.ID)
		if ci > bound || s.cmp > bound {
			return false
		}
	}
	return true
}
