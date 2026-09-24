// Package name 提供内存中的命名空间（名字集合）与名字合法性校验。
package name

import (
	"errors"
	"sort"
	"sync"
	"unicode/utf8"
)

// 名字合法性：任意合法 UTF-8 串，空串合法，路径分隔符按普通字符处理。
func Valid(s string) bool { return utf8.ValidString(s) }

var (
	ErrNotExist = errors.New("name: 旧名不存在")
	ErrExist    = errors.New("name: 新名已存在")
)

// Namespace 是名字集合。批量执行方可用 Lock/Unlock 持锁后调用 RenameLocked。
type Namespace struct {
	mu  sync.Mutex
	set map[string]struct{}
}

func New(names ...string) *Namespace {
	n := &Namespace{set: make(map[string]struct{}, len(names))}
	for _, s := range names {
		n.set[s] = struct{}{}
	}
	return n
}

// Has 不加锁；调用方需保证无并发写，或已持有锁。
func (n *Namespace) Has(s string) bool {
	_, ok := n.set[s]
	return ok
}

func (n *Namespace) Len() int { return len(n.set) }

func (n *Namespace) Lock()   { n.mu.Lock() }
func (n *Namespace) Unlock() { n.mu.Unlock() }

// RenameLocked 在调用方持锁的前提下执行单个重命名。
func (n *Namespace) RenameLocked(old, new string) error {
	if _, ok := n.set[old]; !ok {
		return ErrNotExist
	}
	if _, ok := n.set[new]; ok {
		return ErrExist
	}
	delete(n.set, old)
	n.set[new] = struct{}{}
	return nil
}

// Rename 是自带锁的单个重命名，供批量执行之外的并发修改使用。
func (n *Namespace) Rename(old, new string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.RenameLocked(old, new)
}

// Snapshot 返回排序后的名字切片，用于逐元素比对。
func (n *Namespace) Snapshot() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]string, 0, len(n.set))
	for s := range n.set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
