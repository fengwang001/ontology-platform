package slotting

import (
	"math/rand"
)

// LessFunc 定义键的全序关系（与 map 去重配合使用）。
type LessFunc[K any] func(a, b K) bool

// orderedSet 是按给定全序维护的有序集合。
// 采用确定性种子的 treap：相同操作序列产生完全相同的树形态与遍历结果。
type orderedSet[K any] struct {
	root *treapNode[K]
	rng  *rand.Rand
	less LessFunc[K]
}

type treapNode[K any] struct {
	key         K
	priority    uint64
	left, right *treapNode[K]
}

func newOrderedSet[K any](less LessFunc[K]) *orderedSet[K] {
	return &orderedSet[K]{rng: rand.New(rand.NewSource(1)), less: less}
}

// insert 插入键；已存在时不变。
func (s *orderedSet[K]) insert(key K) {
	s.root = treapInsert(s.root, key, s.rng, s.less)
}

// erase 删除键；不存在时不变。
func (s *orderedSet[K]) erase(key K) {
	s.root = treapErase(s.root, key, s.less)
}

func (s *orderedSet[K]) contains(key K) bool {
	return treapContains(s.root, key, s.less)
}

// min 返回字典序最小键与是否存在。
func (s *orderedSet[K]) min() (K, bool) {
	if s.root == nil {
		var zero K
		return zero, false
	}
	n := s.root
	for n.left != nil {
		n = n.left
	}
	return n.key, true
}

// ascending 按字典序追加全部键到 dst。
func (s *orderedSet[K]) ascending(dst []K) []K {
	return treapWalk(s.root, dst)
}

func treapWalk[K any](n *treapNode[K], dst []K) []K {
	if n == nil {
		return dst
	}
	dst = treapWalk(n.left, dst)
	dst = append(dst, n.key)
	return treapWalk(n.right, dst)
}

func treapContains[K any](n *treapNode[K], key K, less LessFunc[K]) bool {
	for n != nil {
		switch {
		case less(key, n.key):
			n = n.left
		case less(n.key, key):
			n = n.right
		default:
			return true
		}
	}
	return false
}

func treapInsert[K any](n *treapNode[K], key K, rng *rand.Rand, less LessFunc[K]) *treapNode[K] {
	if n == nil {
		return &treapNode[K]{key: key, priority: rng.Uint64()}
	}
	if !less(key, n.key) && !less(n.key, key) {
		return n
	}
	if less(key, n.key) {
		n.left = treapInsert(n.left, key, rng, less)
		if n.left.priority < n.priority {
			n = rotateRight(n)
		}
	} else {
		n.right = treapInsert(n.right, key, rng, less)
		if n.right.priority < n.priority {
			n = rotateLeft(n)
		}
	}
	return n
}

func treapErase[K any](n *treapNode[K], key K, less LessFunc[K]) *treapNode[K] {
	if n == nil {
		return nil
	}
	switch {
	case less(key, n.key):
		n.left = treapErase(n.left, key, less)
	case less(n.key, key):
		n.right = treapErase(n.right, key, less)
	default:
		switch {
		case n.left == nil:
			return n.right
		case n.right == nil:
			return n.left
		default:
			if n.left.priority < n.right.priority {
				n = rotateRight(n)
				n.right = treapErase(n.right, key, less)
			} else {
				n = rotateLeft(n)
				n.left = treapErase(n.left, key, less)
			}
		}
	}
	return n
}

func rotateLeft[K any](n *treapNode[K]) *treapNode[K] {
	r := n.right
	n.right = r.left
	r.left = n
	return r
}

func rotateRight[K any](n *treapNode[K]) *treapNode[K] {
	l := n.left
	n.left = l.right
	l.right = n
	return l
}
