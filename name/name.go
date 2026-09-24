// Package name 提供内存中的命名空间（名字集合）与合法性校验。
package name

import (
	"fmt"
	"slices"
	"sync"
)

// Rename 表示一次重命名请求或一个执行步骤。
type Rename struct {
	Old string
	New string
}

// Valid 报告名字是否合法。本系统接受任意字符串：空串合法，
// 路径分隔符按普通字符处理。校验集中于此入口以便未来收紧。
func Valid(string) bool { return true }

// Namespace 是并发安全的名字集合。
type Namespace struct {
	mu    sync.Mutex
	names map[string]struct{}
}

// New 用给定名字构造命名空间。
func New(names ...string) *Namespace {
	n := &Namespace{names: make(map[string]struct{}, len(names))}
	for _, s := range names {
		n.names[s] = struct{}{}
	}
	return n
}

// Has 报告名字是否存在。
func (n *Namespace) Has(s string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	_, ok := n.names[s]
	return ok
}

// Add 加入名字；已存在时返回 false。
func (n *Namespace) Add(s string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.names[s]; ok {
		return false
	}
	n.names[s] = struct{}{}
	return true
}

// Snapshot 返回排序后的名字副本。
func (n *Namespace) Snapshot() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]string, 0, len(n.names))
	for s := range n.names {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// Equal 报告两个命名空间是否逐元素相同（顺序无关的集合比对）。
func (n *Namespace) Equal(other *Namespace) bool {
	return slices.Equal(n.Snapshot(), other.Snapshot())
}

// Tx 是批量事务视图，仅在 Transact 持锁期间有效。
type Tx struct{ n *Namespace }

// Transact 在持有整把锁的情况下执行 fn，fn 的返回值原样返回。
// 持锁期间其他 goroutine 的 Has/Add 等调用会被阻塞。
func (n *Namespace) Transact(fn func(*Tx) error) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	return fn(&Tx{n: n})
}

// Has 报告名字是否存在（调用方已持锁）。
func (t *Tx) Has(s string) bool {
	_, ok := t.n.names[s]
	return ok
}

// Rename 原子改名：old 必须存在且 new 必须不存在，否则返回错误且不修改。
func (t *Tx) Rename(old, new string) error {
	if _, ok := t.n.names[old]; !ok {
		return fmt.Errorf("name: source %q missing", old)
	}
	if _, ok := t.n.names[new]; ok {
		return fmt.Errorf("name: target %q exists", new)
	}
	delete(t.n.names, old)
	t.n.names[new] = struct{}{}
	return nil
}
