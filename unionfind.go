package ontology

import (
	"fmt"
	"sync"
)

// elem 是并查集中一个元素的内部节点。
// min 仅在节点为根（parent 指向自身）时有效，
// 记录该等价类中字典序最小的 ID。
type elem struct {
	parent string
	rank   int
	min    string
}

// UnionFind 是一个并发安全的等价类合并器（并查集）。
// 零值不可使用，请通过 New 构造。
type UnionFind struct {
	mu     sync.Mutex
	elems  map[string]*elem
	count  int // 当前等价类个数
	hops   uint64
}

// New 创建一个空的 UnionFind。
func New() *UnionFind {
	return &UnionFind{elems: make(map[string]*elem)}
}

// Add 显式创建一个元素。重复 Add 是幂等的，不报错。
// 空串是合法 ID。
func (u *UnionFind) Add(id string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.addLocked(id)
}

// addLocked 在持锁状态下确保元素存在；新建时类数加一。
func (u *UnionFind) addLocked(id string) {
	if _, ok := u.elems[id]; ok {
		return
	}
	u.elems[id] = &elem{parent: id, min: id}
	u.count++
}

// findRootLocked 返回 x 所在树的根 ID 与根节点，并做路径压缩。
// 沿途每解引用一次父指针计一跳，累加到 u.hops。
// 调用方必须持锁且保证 x 已存在。
func (u *UnionFind) findRootLocked(x string) (string, *elem) {
	root := x
	for u.elems[root].parent != root {
		u.hops++
		root = u.elems[root].parent
	}
	// 路径压缩：把沿途节点直接挂到根上。
	for u.elems[x].parent != root {
		next := u.elems[x].parent
		u.elems[x].parent = root
		x = next
	}
	return root, u.elems[root]
}

// Find 返回 x 所在等价类的代表元，即该类中字典序最小的 ID。
// x 从未出现过时返回 ErrUnknownElement，不会隐式创建。
func (u *UnionFind) Find(x string) (string, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if _, ok := u.elems[x]; !ok {
		return "", fmt.Errorf("ontology: Find(%q): %w", x, ErrUnknownElement)
	}
	_, root := u.findRootLocked(x)
	return root.min, nil
}

// Union 把 a、b 并入同一等价类；未出现过的 ID 会被隐式创建。
// 合并按秩进行，根节点始终维护类内最小 ID，
// 因此代表元与合并顺序无关。
func (u *UnionFind) Union(a, b string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.addLocked(a)
	u.addLocked(b)
	raID, ra := u.findRootLocked(a)
	rbID, rb := u.findRootLocked(b)
	if raID == rbID {
		return
	}
	// 按秩合并：矮树挂到高树下。
	if ra.rank < rb.rank {
		raID, ra, rb = rbID, rb, ra
	}
	rb.parent = raID
	if ra.rank == rb.rank {
		ra.rank++
	}
	if rb.min < ra.min {
		ra.min = rb.min
	}
	u.count--
}

// TotalHops 返回自创建以来 Find/Union 内部
// 沿父指针向上查找根所累计的跳数，仅供测试与演示观测复杂度。
func (u *UnionFind) TotalHops() uint64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.hops
}
