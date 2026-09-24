// Package name 提供内存中的名字集合、名字合法性校验与加锁原语。
package name

import (
	"fmt"
	"sort"
	"sync"
)

// Namespace 是一个无序的名字集合。零值不可用，请用 New 构造。
type Namespace struct {
	mu  sync.RWMutex
	set map[string]struct{}
}

// New 用初始名字构造命名空间。
func New(initial ...string) *Namespace {
	ns := &Namespace{set: make(map[string]struct{}, len(initial))}
	for _, n := range initial {
		ns.set[n] = struct{}{}
	}
	return ns
}

// Valid 判定名字是否合法。
// 空串合法；路径分隔符等任何字符都按普通字符处理，因此所有字符串均合法。
func Valid(n string) bool { return true }

// Has 报告名字是否存在（读锁）。
func (ns *Namespace) Has(n string) bool {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	_, ok := ns.set[n]
	return ok
}

// Len 返回名字数量。
func (ns *Namespace) Len() int {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	return len(ns.set)
}

// Snapshot 返回字典序排序后的全部名字，用于逐元素比对。
func (ns *Namespace) Snapshot() []string {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	out := make([]string, 0, len(ns.set))
	for n := range ns.set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Equal 报告两个命名空间是否逐元素相同（顺序无关）。
func (ns *Namespace) Equal(other *Namespace) bool {
	a, b := ns.Snapshot(), other.Snapshot()
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

// Lock / Unlock 供 plan、apply 在整批操作期间独占命名空间。
func (ns *Namespace) Lock()   { ns.mu.Lock() }
func (ns *Namespace) Unlock() { ns.mu.Unlock() }

// RenameLocked 在已持有写锁的前提下搬运一个名字。
// old 必须存在、new 必须不存在，否则返回错误且不修改任何数据（绝不覆盖）。
func (ns *Namespace) RenameLocked(old, newName string) error {
	if _, ok := ns.set[old]; !ok {
		return fmt.Errorf("name: source %q does not exist", old)
	}
	if _, ok := ns.set[newName]; ok && old != newName {
		return fmt.Errorf("name: target %q already exists", newName)
	}
	if old == newName {
		return nil
	}
	delete(ns.set, old)
	ns.set[newName] = struct{}{}
	return nil
}

// Rename 加写锁执行单步搬运（供并发探测测试使用）。
func (ns *Namespace) Rename(old, newName string) error {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	return ns.RenameLocked(old, newName)
}
