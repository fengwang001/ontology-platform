package equivalence

import "sync"

// UnionFind 是字符串 ID 上的并查集，状态保存在进程内存中。
//
// 每个等价类的代表元恒为类中字典序最小的 ID。内部使用按秩合并与
// （全）路径压缩；所有方法均可被多协程并发调用。
type UnionFind struct {
	mu sync.Mutex

	// parent[x] 为 x 的父指针；根的 parent 指向自身。
	parent map[string]string
	// rank 仅对根有意义，用于按秩合并。
	rank map[string]int
	// minimum 仅对根有意义，记录该类字典序最小的 ID。
	minimum map[string]string

	// hopCount 仅用于测试/演示：累计 Find 沿父指针上行访问的跳数。
	hopCount int64
}

// New 创建一个空的合并器。
func New() *UnionFind {
	return &UnionFind{
		parent:  make(map[string]string),
		rank:    make(map[string]int),
		minimum: make(map[string]string),
	}
}

// Add 显式创建一个单元素等价类。重复 Add 幂等，不会报错。
// 空串是合法 ID。
func (u *UnionFind) Add(id string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.addLocked(id)
}

func (u *UnionFind) addLocked(id string) {
	if _, ok := u.parent[id]; ok {
		return
	}
	u.parent[id] = id
	u.rank[id] = 0
	u.minimum[id] = id
}

// findLocked 返回 x 所在树的根（即最小 ID），执行全路径压缩，
// 并把上行阶段走过的父指针跳数计入 hopCount。
// 调用方必须持有 u.mu，且 x 必须已存在。
func (u *UnionFind) findLocked(x string) string {
	node := x
	for u.parent[node] != node {
		node = u.parent[node]
		u.hopCount++
	}
	root := node

	// 第二趟：把路径上的所有节点直接挂到根，完成全路径压缩。
	for u.parent[x] != root {
		next := u.parent[x]
		u.parent[x] = root
		x = next
	}
	return root
}

// Find 返回 id 所在等价类的代表元（类中字典序最小的 ID）。
// id 从未出现时返回包装了 ErrUnknownElement 的错误，不会隐式创建。
func (u *UnionFind) Find(id string) (string, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	if _, ok := u.parent[id]; !ok {
		return "", ErrUnknownElement
	}
	root := u.findLocked(id)
	return u.minimum[root], nil
}
