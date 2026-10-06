package endpointshard

import (
	"math/rand"
	"sync/atomic"
)

// orderedSet 是基于 treap（随机优先级二叉搜索树）的有序集合。
//
// 用于三类索引：就绪/回退端点索引（按标识排序）、
// 落位索引（未满分片按 端点数降序、编号升序）、
// 合并索引（全部分片按 端点数升序、编号升序）。
// 插入、删除、取最小元的期望复杂度为 O(log n)，
// 中序遍历与结果大小成正比。优先级由调用方注入的随机源产生，
// 只影响树的内部形状，不影响任何可观察输出。
type orderedSet[K any] struct {
	less func(a, b K) bool
	rng  *rand.Rand
	root *treapNode[K]
	size int
}

type treapNode[K any] struct {
	key         K
	priority    uint64
	left, right *treapNode[K]
}

func newOrderedSet[K any](less func(a, b K) bool, rng *rand.Rand) *orderedSet[K] {
	return &orderedSet[K]{less: less, rng: rng}
}

// Len 返回集合大小，O(1)。
func (s *orderedSet[K]) Len() int { return s.size }

// Insert 插入 key；若已存在则不改变集合。返回是否真正插入。
// ctr 非空时按节点访问次数计数。
func (s *orderedSet[K]) Insert(key K, ctr *atomic.Uint64) bool {
	var inserted bool
	s.root, inserted = s.insert(s.root, key, s.rng.Uint64(), ctr)
	if inserted {
		s.size++
	}
	return inserted
}

func (s *orderedSet[K]) insert(n *treapNode[K], key K, prio uint64, ctr *atomic.Uint64) (*treapNode[K], bool) {
	if n == nil {
		return &treapNode[K]{key: key, priority: prio}, true
	}
	count(ctr)
	if s.less(key, n.key) {
		child, inserted := s.insert(n.left, key, prio, ctr)
		n.left = child
		if child.priority < n.priority { // 右旋，提升子节点
			n.left = child.right
			child.right = n
			return child, inserted
		}
		return n, inserted
	}
	if s.less(n.key, key) {
		child, inserted := s.insert(n.right, key, prio, ctr)
		n.right = child
		if child.priority < n.priority { // 左旋，提升子节点
			n.right = child.left
			child.left = n
			return child, inserted
		}
		return n, inserted
	}
	return n, false
}

// Remove 删除 key；若不存在则不改变集合。返回是否真正删除。
func (s *orderedSet[K]) Remove(key K, ctr *atomic.Uint64) bool {
	var removed bool
	s.root, removed = s.remove(s.root, key, ctr)
	if removed {
		s.size--
	}
	return removed
}

func (s *orderedSet[K]) remove(n *treapNode[K], key K, ctr *atomic.Uint64) (*treapNode[K], bool) {
	if n == nil {
		return nil, false
	}
	count(ctr)
	if s.less(key, n.key) {
		child, removed := s.remove(n.left, key, ctr)
		n.left = child
		return n, removed
	}
	if s.less(n.key, key) {
		child, removed := s.remove(n.right, key, ctr)
		n.right = child
		return n, removed
	}
	return s.mergeTrees(n.left, n.right, ctr), true
}

func (s *orderedSet[K]) mergeTrees(l, r *treapNode[K], ctr *atomic.Uint64) *treapNode[K] {
	if l == nil {
		return r
	}
	if r == nil {
		return l
	}
	count(ctr)
	if l.priority < r.priority {
		return &treapNode[K]{key: l.key, priority: l.priority, left: l.left, right: s.mergeTrees(l.right, r, ctr)}
	}
	return &treapNode[K]{key: r.key, priority: r.priority, left: s.mergeTrees(l, r.left, ctr), right: r.right}
}

// Min 返回最小键；ok 为 false 表示集合为空。
func (s *orderedSet[K]) Min(ctr *atomic.Uint64) (key K, ok bool) {
	n := s.root
	if n == nil {
		return key, false
	}
	for n.left != nil {
		count(ctr)
		n = n.left
	}
	count(ctr)
	return n.key, true
}

// FirstTwo 按升序返回最小的至多两个键。
func (s *orderedSet[K]) FirstTwo(ctr *atomic.Uint64) []K {
	out := make([]K, 0, 2)
	s.Ascend(ctr, func(k K) bool {
		out = append(out, k)
		return len(out) < 2
	})
	return out
}

// Ascend 按升序遍历；fn 返回 false 时提前停止。
// 访问节点数与遍历的键数成正比。
func (s *orderedSet[K]) Ascend(ctr *atomic.Uint64, fn func(K) bool) {
	ascend(s.root, ctr, fn)
}

func ascend[K any](n *treapNode[K], ctr *atomic.Uint64, fn func(K) bool) bool {
	if n == nil {
		return true
	}
	count(ctr)
	if !ascend(n.left, ctr, fn) {
		return false
	}
	if !fn(n.key) {
		return false
	}
	return ascend(n.right, ctr, fn)
}

// AscendFrom 从第一个不小于 from 的键开始按升序遍历；
// fn 返回 false 时提前停止。
func (s *orderedSet[K]) AscendFrom(from K, ctr *atomic.Uint64, fn func(K) bool) {
	s.ascendFrom(s.root, from, ctr, fn)
}

func (s *orderedSet[K]) ascendFrom(n *treapNode[K], from K, ctr *atomic.Uint64, fn func(K) bool) bool {
	if n == nil {
		return true
	}
	count(ctr)
	if s.less(n.key, from) {
		return s.ascendFrom(n.right, from, ctr, fn)
	}
	if !s.ascendFrom(n.left, from, ctr, fn) {
		return false
	}
	if !fn(n.key) {
		return false
	}
	return ascend(n.right, ctr, fn)
}

func count(ctr *atomic.Uint64) {
	if ctr != nil {
		ctr.Add(1)
	}
}
