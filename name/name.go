// Package name 提供内存命名空间（名字集合）与合法性校验。
package name

import (
	"errors"
	"sort"
	"sync"
)

// ErrExists 表示目标名已存在（改名会覆盖）。
var ErrExists = errors.New("name: target already exists")

// ErrMissing 表示源名不存在。
var ErrMissing = errors.New("name: source does not exist")

// Set 是名字集合类型。
type Set = map[string]struct{}

// Namespace 是带读写锁的内存名字集合。整批改在写锁内完成，
// 并发修改将阻塞等待，不会与批量执行交错。
type Namespace struct {
	mu sync.RWMutex
	ns Set
	// Lookups 记录底层 map 读次数，仅用于复杂度论证测试。
	Lookups int
}

// New 用初始名字集合构造命名空间（复制入参）。
func New(initial []string) *Namespace {
	n := &Namespace{ns: make(Set, len(initial))}
	for _, s := range initial {
		n.ns[s] = struct{}{}
	}
	return n
}

// Valid 判定名字是否合法：任意字符串均合法（含空串与路径分隔符）。
func Valid(s string) bool { return true }

// Lock / Unlock 供上层批量操作持锁。
func (n *Namespace) Lock()   { n.mu.Lock() }
func (n *Namespace) Unlock() { n.mu.Unlock() }

// HasLocked 在已持锁状态下判断名字是否存在。
func (n *Namespace) HasLocked(s string) bool {
	n.Lookups++
	_, ok := n.ns[s]
	return ok
}

// RenameLocked 在已持锁状态下原子改名。force 为真时允许覆盖已存在目标，
// 用于撤销逆序恢复；否则目标存在返回 ErrExists，源不存在返回 ErrMissing。
func (n *Namespace) RenameLocked(from, to string, force bool) error {
	n.Lookups++
	if _, ok := n.ns[from]; !ok {
		return ErrMissing
	}
	n.Lookups++
	if _, ok := n.ns[to]; ok && !force {
		return ErrExists
	}
	delete(n.ns, from)
	n.ns[to] = struct{}{}
	return nil
}

// Snapshot 返回当前名字集合的有序副本。
func (n *Namespace) Snapshot() []string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	out := make([]string, 0, len(n.ns))
	for s := range n.ns {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// SnapshotLocked 在已持锁状态下返回有序副本。
func (n *Namespace) SnapshotLocked() []string {
	out := make([]string, 0, len(n.ns))
	for s := range n.ns {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// Equal 做顺序无关的集合相等比较。
func Equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}
