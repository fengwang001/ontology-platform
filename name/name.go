// Package name 提供内存中的命名空间（名字集合）与合法性校验。
package name

import (
	"sort"
	"sync"
)

// Namespace 是一个带锁的名字集合。
// 所有字符串均为合法名字：空串合法，路径分隔符按普通字符处理。
type Namespace struct {
	mu sync.Mutex
	// 计数器仅供复杂度测试读取（测试同包，可直接访问）。
	lookups int
	set     map[string]struct{}
}

// New 用初始名字集合构造命名空间。
func New(initial ...string) *Namespace {
	ns := &Namespace{set: make(map[string]struct{}, len(initial))}
	for _, n := range initial {
		ns.set[n] = struct{}{}
	}
	return ns
}

// Valid 报告名字是否合法。本实现中任何字符串（含空串）都合法。
func Valid(n string) bool { return true }

// Lock / Unlock 供批量执行期间持有写锁，阻塞并发修改。
func (ns *Namespace) Lock()   { ns.mu.Lock() }
func (ns *Namespace) Unlock() { ns.mu.Unlock() }

// TryLock 尝试加锁，失败立即返回 false（用于“拒绝并发修改”语义）。
func (ns *Namespace) TryLock() bool { return ns.mu.TryLock() }

// HasLocked 报告 n 是否存在；调用方必须持锁。
func (ns *Namespace) HasLocked(n string) bool {
	ns.lookups++
	_, ok := ns.set[n]
	return ok
}

// Has 是加锁版本的存在性查询。
func (ns *Namespace) Has(n string) bool {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	return ns.HasLocked(n)
}

// renameLocked 执行原子的 old -> new。
// 成功的前提是 old 存在、new 不存在（new==old 时为无操作）。
// 调用方必须持锁。
func (ns *Namespace) renameLocked(old, new string) error {
	if !ns.HasLocked(old) {
		return &OpError{Op: "rename", Name: old, Err: ErrMissing}
	}
	if old == new {
		return nil
	}
	if ns.HasLocked(new) {
		return &OpError{Op: "rename", Name: new, Err: ErrExists}
	}
	delete(ns.set, old)
	ns.set[new] = struct{}{}
	return nil
}

// RenameLocked 加锁并执行一次重命名，目标已存在时返回 ErrExists 包装错误。
func (ns *Namespace) RenameLocked(old, new string) error {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	return ns.renameLocked(old, new)
}

// RenameWhileLocked 在调用方已持锁时执行一次重命名。
func (ns *Namespace) RenameWhileLocked(old, new string) error {
	return ns.renameLocked(old, new)
}

// SnapshotLocked 返回排序后的全部名字；调用方必须持锁。
func (ns *Namespace) SnapshotLocked() []string {
	out := make([]string, 0, len(ns.set))
	for k := range ns.set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Snapshot 是加锁版本的快照。
func (ns *Namespace) Snapshot() []string {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	return ns.SnapshotLocked()
}

// EqualSet 报告两个排序快照是否逐元素相同（顺序无关集合比对）。
func EqualSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := make(map[string]struct{}, len(a))
	for _, s := range a {
		m[s] = struct{}{}
	}
	for i := range a {
		if _, ok := m[b[i]]; !ok {
			return false
		}
	}
	return true
}

// Lookups 返回累计的集合查找次数（复杂度测试用）。
func (ns *Namespace) Lookups() int {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	return ns.lookups
}

// 重置查找计数器（复杂度测试用）。
func (ns *Namespace) resetLookups() { ns.lookups = 0 }

var (
	// ErrExists 表示目标名已存在。
	ErrExists = opErr("name: target exists")
	// ErrMissing 表示旧名不存在。
	ErrMissing = opErr("name: source missing")
)

type opErr string

func (e opErr) Error() string { return string(e) }

// OpError 携带操作类型与出错名字，便于判定与展示。
type OpError struct {
	Op   string
	Name string
	Err  error
}

func (e *OpError) Error() string { return e.Op + " " + e.Name + ": " + e.Err.Error() }
func (e *OpError) Unwrap() error { return e.Err }
