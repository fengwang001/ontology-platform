// Package rank 维护单个 Key 的存活行有序集合，按排序键 (T, ID) 升序。
// 底层是 treap（随机化 BST，仅用标准库 math/rand），增删取均期望 O(log n)。
package rank

import "math/rand"

// Row 是一行存活数据：ID 全局唯一，T 可为负。
type Row struct {
	ID string
	T  int64
}

type node struct {
	row         Row
	prio        uint64
	left, right *node
}

// Set 是按 (T, ID) 升序排列的行集合。cmp 为非导出计数器，记录最近一次
// Insert/Delete 的排序键比较次数，仅供本包白盒测试，不进入公开接口。
type Set struct {
	root *node
	n    int
	cmp  int
	rng  *rand.Rand
}

// New 返回空集合。
func New() *Set { return &Set{rng: rand.New(rand.NewSource(1))} }

// compare 按 (T, ID) 比较：先 T，T 相等再比 ID 字节字典序；每次比较计数加一。
func (s *Set) compare(a, b Row) int {
	s.cmp++
	switch {
	case a.T < b.T:
		return -1
	case a.T > b.T:
		return 1
	case a.ID < b.ID:
		return -1
	case a.ID > b.ID:
		return 1
	default:
		return 0
	}
}

func (s *Set) rotR(t *node) *node { l := t.left; t.left = l.right; l.right = t; return l }
func (s *Set) rotL(t *node) *node { r := t.right; t.right = r.left; r.left = t; return r }

func (s *Set) insert(t, x *node) *node {
	if t == nil {
		s.n++
		return x
	}
	switch c := s.compare(x.row, t.row); {
	case c < 0:
		t.left = s.insert(t.left, x)
		if t.left.prio < t.prio {
			t = s.rotR(t)
		}
	case c > 0:
		t.right = s.insert(t.right, x)
		if t.right.prio < t.prio {
			t = s.rotL(t)
		}
	}
	return t // c==0 即同 ID 已存在（上层已拦截），不重复插入
}

// Insert 按 (T, ID) 插入一行。
func (s *Set) Insert(r Row) {
	s.cmp = 0
	s.root = s.insert(s.root, &node{row: r, prio: s.rng.Uint64()})
}

func (s *Set) erase(t *node, r Row) (*node, bool) {
	if t == nil {
		return nil, false
	}
	switch c := s.compare(r, t.row); {
	case c < 0:
		var ok bool
		t.left, ok = s.erase(t.left, r)
		return t, ok
	case c > 0:
		var ok bool
		t.right, ok = s.erase(t.right, r)
		return t, ok
	}
	s.n--
	switch {
	case t.left == nil:
		return t.right, true
	case t.right == nil:
		return t.left, true
	case t.left.prio > t.right.prio: // 右孩子堆序更优先（prio 更小），先左旋
		t = s.rotL(t)
		t.left, _ = s.erase(t.left, r)
	default:
		t = s.rotR(t)
		t.right, _ = s.erase(t.right, r)
	}
	return t, true
}

// Delete 按 r.ID（连同 T 精确定位）删除一行，报告该行是否存在。
func (s *Set) Delete(r Row) bool {
	s.cmp = 0
	var ok bool
	s.root, ok = s.erase(s.root, r)
	return ok
}

// Min 返回排序键最小的一行；空集合时 ok 为 false。沿最左链下行，不做比较。
func (s *Set) Min() (Row, bool) {
	t := s.root
	if t == nil {
		return Row{}, false
	}
	for t.left != nil {
		t = t.left
	}
	return t.row, true
}

// Len 返回存活行数。
func (s *Set) Len() int { return s.n }
