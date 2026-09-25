// Package name 提供内存中的命名空间（名字集合）与合法性校验。
//
// 合法性定义：任意字符串都是合法名字，包括空串与含路径分隔符的名字
// （分隔符按普通字符处理）。校验集中在请求层（见 plan 包）。
package name

import (
	"sort"
	"sync"
)

// Valid 报告名字是否合法。按设计任意字符串均合法。
func Valid(s string) bool { return true }

// NS 是并发安全的命名空间。批量执行期间应通过 Lock 持锁，
// 并改用 Locked 系列方法操作，公开的 Add/Remove/Has 会被阻塞。
type NS struct {
	mu  sync.Mutex
	set map[string]struct{}
}

// New 用给定名字构造命名空间。
func New(names ...string) *NS {
	n := &NS{set: make(map[string]struct{}, len(names))}
	for _, s := range names {
		n.set[s] = struct{}{}
	}
	return n
}

// Lock 锁定命名空间，供批量执行独占使用。
func (n *NS) Lock() { n.mu.Lock() }

// Unlock 解除 Lock 加的锁。
func (n *NS) Unlock() { n.mu.Unlock() }

// Has 报告名字是否存在。
func (n *NS) Has(s string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.HasLocked(s)
}

// Add 加入一个名字。
func (n *NS) Add(s string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.set[s] = struct{}{}
}

// Remove 移除一个名字。
func (n *NS) Remove(s string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.set, s)
}

// HasLocked 同 Has，但调用方必须已持有 Lock。
func (n *NS) HasLocked(s string) bool {
	_, ok := n.set[s]
	return ok
}

// RenameLocked 把 old 改名为 new，调用方必须已持有 Lock。
// old 不存在或 new 已存在时返回 false，不做任何修改。
func (n *NS) RenameLocked(old, new string) bool {
	if _, ok := n.set[old]; !ok {
		return false
	}
	if _, ok := n.set[new]; ok {
		return false
	}
	delete(n.set, old)
	n.set[new] = struct{}{}
	return true
}

// Snapshot 返回排序后的名字列表，用于逐元素比对。
func (n *NS) Snapshot() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]string, 0, len(n.set))
	for s := range n.set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// Equal 报告两个排序快照是否逐元素相同（顺序无关的集合比对）。
func Equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
