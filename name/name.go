// Package name 提供内存中的命名空间与名字合法性校验。
//
// 命名空间是一个名字集合，所有变更方法都要求调用方持有锁；
// 批量执行期间持锁，因此并发修改会被阻塞到批次结束（见 DESIGN.md）。
package name

import "sync"

// Valid 校验名字合法性。按设计，任何字符串都是合法名字：
// 空串合法，路径分隔符按普通字符处理。
func Valid(s string) bool { return true }

// Namespace 是内存中的名字集合。
type Namespace struct {
	mu    sync.Mutex
	names map[string]struct{}
}

// New 用给定初始名字创建命名空间。
func New(names ...string) *Namespace {
	ns := &Namespace{names: make(map[string]struct{}, len(names))}
	for _, s := range names {
		ns.names[s] = struct{}{}
	}
	return ns
}

// Lock 锁定命名空间，批量执行期间必须持有。
func (n *Namespace) Lock() { n.mu.Lock() }

// Unlock 解锁命名空间。
func (n *Namespace) Unlock() { n.mu.Unlock() }

// Has 报告名字是否存在。调用方须持有锁。
func (n *Namespace) Has(s string) bool {
	_, ok := n.names[s]
	return ok
}

// Add 加入名字。调用方须持有锁并保证名字不存在。
func (n *Namespace) Add(s string) { n.names[s] = struct{}{} }

// Remove 移除名字。调用方须持有锁并保证名字存在。
func (n *Namespace) Remove(s string) { delete(n.names, s) }

// Rename 执行单步 o→t：要求 o 存在且 t 不存在，否则返回 false 且不修改。
// 调用方须持有锁。
func (n *Namespace) Rename(o, t string) bool {
	if _, ok := n.names[o]; !ok {
		return false
	}
	if _, clash := n.names[t]; clash {
		return false
	}
	delete(n.names, o)
	n.names[t] = struct{}{}
	return true
}

// Len 返回名字数量。调用方须持有锁。
func (n *Namespace) Len() int { return len(n.names) }

// Snapshot 返回当前名字集合的副本（内部加锁）。
func (n *Namespace) Snapshot() map[string]bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make(map[string]bool, len(n.names))
	for s := range n.names {
		out[s] = true
	}
	return out
}

// Equal 报告当前集合与给定集合是否逐元素相同（顺序无关）。
func Equal(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for s := range a {
		if !b[s] {
			return false
		}
	}
	return true
}
