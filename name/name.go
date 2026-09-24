// Package name 维护内存中的名字集合（命名空间）并提供合法性与改名原语。
package name

import (
	"errors"
	"sort"
	"sync"
)

// ErrLocked 表示命名空间已被占用，TryLock 失败时由调用方包装返回。
var ErrLocked = errors.New("namespace: locked by another operation")

// 改名原语的可判定错误。
var (
	ErrNotFound = errors.New("namespace: old name does not exist")
	ErrExists   = errors.New("namespace: target name already exists")
)

// Namespace 是顺序无关的名字集合，所有访问都由互斥锁保护。
type Namespace struct {
	mu sync.Mutex
	m  map[string]struct{}
}

// New 用初始名字构造命名空间。
func New(initial ...string) *Namespace {
	ns := &Namespace{m: make(map[string]struct{}, len(initial))}
	for _, n := range initial {
		ns.m[n] = struct{}{}
	}
	return ns
}

// Valid 判定名字合法性：任意字符串都合法（空串合法、路径分隔符是普通字符）。
func Valid(s string) bool { return true }

// Lock / Unlock 供批量执行在整个事务期间持锁。
func (ns *Namespace) Lock()      { ns.mu.Lock() }
func (ns *Namespace) Unlock()    { ns.mu.Unlock() }
func (ns *Namespace) TryLock() bool { return ns.mu.TryLock() }

// Contains 报告名字是否存在（内部查找计入计数器由上层 plan 自行统计）。
func (ns *Namespace) Contains(n string) bool {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	return ns.containsLocked(n)
}

func (ns *Namespace) containsLocked(n string) bool {
	_, ok := ns.m[n]
	return ok
}

// ContainsLocked 假定调用方已持锁。
func (ns *Namespace) ContainsLocked(n string) bool { return ns.containsLocked(n) }

// MoveLocked 执行一次原子改名，要求 old 存在且 new 不存在。调用方必须持锁。
func (ns *Namespace) MoveLocked(oldName, newName string) error {
	if !ns.containsLocked(oldName) {
		return ErrNotFound
	}
	if newName != oldName && ns.containsLocked(newName) {
		return ErrExists
	}
	delete(ns.m, oldName)
	ns.m[newName] = struct{}{}
	return nil
}

// UndoMoveLocked 是 MoveLocked 的逆操作（new 存在、old 不存在）。调用方必须持锁。
func (ns *Namespace) UndoMoveLocked(oldName, newName string) error {
	if !ns.containsLocked(newName) {
		return ErrNotFound
	}
	if oldName != newName && ns.containsLocked(oldName) {
		return ErrExists
	}
	delete(ns.m, newName)
	ns.m[oldName] = struct{}{}
	return nil
}

// Snapshot 返回排序后的全部名字（顺序无关集合的确定性表示）。
func (ns *Namespace) Snapshot() []string {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	out := make([]string, 0, len(ns.m))
	for n := range ns.m {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Equal 比较两个名字集合是否逐元素相同。
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
