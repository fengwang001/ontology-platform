// Package name 提供内存中的名字集合与合法性校验。
package name

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

var (
	// ErrInvalidName 名字含 NUL 字节（唯一非法情形）；空串与路径分隔符均合法。
	ErrInvalidName = errors.New("name: invalid name (contains NUL)")
	// ErrNotFound 旧名不存在。
	ErrNotFound = errors.New("name: not found")
	// ErrExists 目标名已存在（直接移动会覆盖）。
	ErrExists = errors.New("name: already exists")
)

// Space 是一个带锁的名字集合。
type Space struct {
	mu    sync.Mutex
	names map[string]struct{}
}

// New 用初始名字集合构造空间。
func New(initial ...string) *Space {
	s := &Space{names: make(map[string]struct{}, len(initial))}
	for _, n := range initial {
		s.names[n] = struct{}{}
	}
	return s
}

// Valid 判定名字合法性：除 NUL 外一切字符（含空串、'/'）均为普通字符。
func Valid(n string) bool { return !strings.ContainsRune(n, 0) }

// Lock / Unlock 暴露内部锁，供批量执行期间独占命名空间。
func (s *Space) Lock()   { s.mu.Lock() }
func (s *Space) Unlock() { s.mu.Unlock() }

// HasLocked 在已持锁前提下判定名字是否存在。
func (s *Space) HasLocked(n string) bool { _, ok := s.names[n]; return ok }

// Has 加锁判定名字是否存在。
func (s *Space) Has(n string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.HasLocked(n)
}

// AddLocked 在已持锁前提下加入一个名字，已存在或非法时报错。
func (s *Space) AddLocked(n string) error {
	if !Valid(n) {
		return ErrInvalidName
	}
	if s.HasLocked(n) {
		return ErrExists
	}
	s.names[n] = struct{}{}
	return nil
}

// RemoveLocked 在已持锁前提下删除一个名字，不存在时报错。
func (s *Space) RemoveLocked(n string) error {
	if _, ok := s.names[n]; !ok {
		return ErrNotFound
	}
	delete(s.names, n)
	return nil
}

// RenameLocked 原子地把 old 改为 new：两者都校验，且要求 old 存在、new 不存在，
// 因此任何覆盖都不可能发生。
func (s *Space) RenameLocked(old, newName string) error {
	if !Valid(old) || !Valid(newName) {
		return ErrInvalidName
	}
	if !s.HasLocked(old) {
		return ErrNotFound
	}
	if newName != old && s.HasLocked(newName) {
		return ErrExists
	}
	if newName == old {
		return nil
	}
	delete(s.names, old)
	s.names[newName] = struct{}{}
	return nil
}

// Snapshot 返回排序后的名字切片（顺序确定，便于逐元素比对）。
func (s *Space) Snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.names))
	for n := range s.names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Equal 报告两个排序（或任意）名字集合是否逐元素相同（顺序无关）。
func Equal(a, b []string) bool {
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}
