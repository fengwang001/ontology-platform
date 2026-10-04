// Package indexreg 维护搜索集群的索引登记与开闭状态。
package indexreg

import (
	"errors"
	"sort"
	"sync"
)

// 可由 errors.Is 区分的哨兵错误。
var (
	// ErrInvalidName 表示名字不符合命名规则。
	ErrInvalidName = errors.New("indexreg: invalid name")
	// ErrNameConflict 表示名字与既有索引或别名冲突。
	ErrNameConflict = errors.New("indexreg: name conflict")
	// ErrIndexNotFound 表示索引不存在。
	ErrIndexNotFound = errors.New("indexreg: index not found")
	// ErrIndexClosed 表示索引处于关闭状态。
	ErrIndexClosed = errors.New("indexreg: index is closed")
)

// ValidName 判定名字是否合法：1 到 64 字节，小写字母、数字、
// 连字符或下划线，且不以连字符或下划线开头。
func ValidName(name string) bool {
	n := len(name)
	if n < 1 || n > 64 {
		return false
	}
	if name[0] == '-' || name[0] == '_' {
		return false
	}
	for i := 0; i < n; i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '-' || c == '_':
		default:
			return false
		}
	}
	return true
}

// State 表示索引的开闭状态。
type State uint8

const (
	// Open 表示索引可读写。
	Open State = 1
	// Closed 表示索引已关闭。
	Closed State = 2
)

// Registry 是索引登记表，也是集群纪元（epoch）的唯一所有者。
// 零值不可用，须用 New 构造。
type Registry struct {
	mu    sync.RWMutex
	idx   map[string]State
	epoch uint64
}

// New 创建一个空的索引登记表。
func New() *Registry { return &Registry{idx: map[string]State{}} }

// Lock / Unlock 暴露内部互斥锁，供需要把“索引 + 别名”作为一个
// 原子单元提交的组合方（alias.Manager）使用。独立使用本包时无需调用。
func (r *Registry) Lock()   { r.mu.Lock() }
func (r *Registry) Unlock() { r.mu.Unlock() }

// Epoch 返回当前纪元（自 0 起）。
func (r *Registry) Epoch() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.epoch
}

// CreateIndex 登记一个新索引。
// 拒绝次序：参数非法 > 与现有索引同名冲突；别名维度的冲突由组合方另行检查。
func (r *Registry) CreateIndex(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.CreateLocked(name)
}

// CloseIndex 关闭索引；不存在报索引不存在，已关闭为空操作。
func (r *Registry) CloseIndex(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.CloseLocked(name)
}

// OpenIndex 打开索引；不存在报索引不存在，已打开为空操作。
func (r *Registry) OpenIndex(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.OpenLocked(name)
}

// StateOf 返回索引状态。
func (r *Registry) StateOf(name string) (State, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.StateUnlocked(name)
}

// Names 返回所有索引名（字节序）。
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.idx))
	for name := range r.idx {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// 以下 *Locked / *Unlocked 方法假定调用方已持有写锁（或读锁，仅限只读方法）。

// CreateLocked 是 CreateIndex 的不加锁版本。
func (r *Registry) CreateLocked(name string) error {
	if !ValidName(name) {
		return ErrInvalidName
	}
	if _, exists := r.idx[name]; exists {
		return ErrNameConflict
	}
	r.idx[name] = Open
	r.epoch++
	return nil
}

// CloseLocked 是 CloseIndex 的不加锁版本。
func (r *Registry) CloseLocked(name string) error {
	st, ok := r.idx[name]
	if !ok {
		return ErrIndexNotFound
	}
	if st == Closed {
		return nil
	}
	r.idx[name] = Closed
	r.epoch++
	return nil
}

// OpenLocked 是 OpenIndex 的不加锁版本。
func (r *Registry) OpenLocked(name string) error {
	st, ok := r.idx[name]
	if !ok {
		return ErrIndexNotFound
	}
	if st == Open {
		return nil
	}
	r.idx[name] = Open
	r.epoch++
	return nil
}

// StateUnlocked 是 StateOf 的不加锁版本。
func (r *Registry) StateUnlocked(name string) (State, bool) {
	st, ok := r.idx[name]
	return st, ok
}

// SnapshotUnlocked 返回索引状态的深拷贝工作副本。
func (r *Registry) SnapshotUnlocked() map[string]State {
	next := make(map[string]State, len(r.idx))
	for name, st := range r.idx {
		next[name] = st
	}
	return next
}

// ReplaceUnlocked 用工作副本整体替换索引状态，本身不推进纪元；
// 是否推进由组合方依据“终态是否变化”统一决定。
func (r *Registry) ReplaceUnlocked(next map[string]State) {
	cp := make(map[string]State, len(next))
	for name, st := range next {
		cp[name] = st
	}
	r.idx = cp
}

// BumpEpochUnlocked 在确有状态改变的被接受操作后推进纪元。
func (r *Registry) BumpEpochUnlocked() { r.epoch++ }
