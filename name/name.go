// Package name 提供内存中的命名空间（名字集合）与名字合法性校验。
package name

import (
	"errors"
	"sort"
	"sync"
)

// ErrInvalidName 表示名字不合法：仅 NUL 字节被禁止。
var ErrInvalidName = errors.New("name: invalid name (NUL byte)")

// Valid 判定名字是否合法。空串合法；路径分隔符按普通字符处理。
func Valid(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			return false
		}
	}
	return true
}

// Namespace 是受互斥锁保护的名字集合。
type Namespace struct {
	mu    sync.RWMutex
	names map[string]struct{}
}

// New 用初始名字构造命名空间。
func New(initial ...string) *Namespace {
	ns := &Namespace{names: make(map[string]struct{}, len(initial))}
	for _, n := range initial {
		ns.names[n] = struct{}{}
	}
	return ns
}

// Lock / RLock 供上层（批量执行）持有整段临界区。
func (ns *Namespace) Lock()    { ns.mu.Lock() }
func (ns *Namespace) Unlock()  { ns.mu.Unlock() }
func (ns *Namespace) RLock()   { ns.mu.RLock() }
func (ns *Namespace) RUnlock() { ns.mu.RUnlock() }

// Has 报告名字是否存在。调用方须自行持有相应锁。
func (ns *Namespace) Has(n string) bool {
	_, ok := ns.names[n]
	return ok
}

// Add 加入一个此前不存在的名字；已存在或非法时返回错误且不修改。
func (ns *Namespace) Add(n string) error {
	if !Valid(n) {
		return ErrInvalidName
	}
	if ns.Has(n) {
		return errors.New("name: already exists: " + n)
	}
	ns.names[n] = struct{}{}
	return nil
}

// Remove 删除一个必须存在的名字；不存在或非法时返回错误且不修改。
func (ns *Namespace) Remove(n string) error {
	if !Valid(n) {
		return ErrInvalidName
	}
	if !ns.Has(n) {
		return errors.New("name: missing: " + n)
	}
	delete(ns.names, n)
	return nil
}

// Len 返回名字数量。
func (ns *Namespace) Len() int { return len(ns.names) }

// Snapshot 返回排序后的名字副本，用于逐元素比对。
func (ns *Namespace) Snapshot() []string {
	out := make([]string, 0, len(ns.names))
	for n := range ns.names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// EqualAsSet 报告两个命名空间是否逐元素（顺序无关）相同。
func EqualAsSet(a, b *Namespace) bool {
	if a.Len() != b.Len() {
		return false
	}
	for n := range a.names {
		if !b.Has(n) {
			return false
		}
	}
	return true
}
