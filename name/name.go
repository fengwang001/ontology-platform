// Package name 提供内存中的命名空间（名字集合）与名字合法性校验。
package name

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

// ErrIllegalName 表示名字非法：仅含 NUL 字符的名字非法，
// 空串与路径分隔符都是合法普通字符。
var ErrIllegalName = errors.New("name: illegal name (contains NUL)")

// Valid 判定名字是否合法。
func Valid(s string) bool { return !strings.ContainsRune(s, '\x00') }

// Space 是并发安全的名字集合。批量执行期间可通过 Lock/Unlock
// 独占整个命名空间，此时并发的 Add/Remove/Has 会被阻塞。
type Space struct {
	mu    sync.Mutex
	names map[string]struct{}
}

// New 用给定名字构造命名空间，非法名字导致报错。
func New(names ...string) (*Space, error) {
	s := &Space{names: make(map[string]struct{}, len(names))}
	for _, n := range names {
		if !Valid(n) {
			return nil, ErrIllegalName
		}
		s.names[n] = struct{}{}
	}
	return s, nil
}

// Must 是 New 的便捷包装，非法名字直接 panic（供测试与演示用）。
func Must(names ...string) *Space {
	s, err := New(names...)
	if err != nil {
		panic(err)
	}
	return s
}

// Lock 独占命名空间，供批量执行期间阻止并发修改。
func (s *Space) Lock() { s.mu.Lock() }

// Unlock 解除独占。
func (s *Space) Unlock() { s.mu.Unlock() }

// Has 报告名字是否存在（加锁）。
func (s *Space) Has(n string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.HasLocked(n)
}

// HasLocked 同 Has，但调用方须已持锁。
func (s *Space) HasLocked(n string) bool {
	_, ok := s.names[n]
	return ok
}

// Add 加入一个名字；已存在或非法时报错。
func (s *Space) Add(n string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.AddLocked(n)
}

// AddLocked 同 Add，但调用方须已持锁。
func (s *Space) AddLocked(n string) error {
	if !Valid(n) {
		return ErrIllegalName
	}
	s.names[n] = struct{}{}
	return nil
}

// RemoveLocked 移除一个名字，调用方须已持锁。
func (s *Space) RemoveLocked(n string) { delete(s.names, n) }

// RenameLocked 把 old 改名为 new，调用方须已持锁。
// old 不存在或 new 已存在（覆盖）时返回错误，集合不变。
func (s *Space) RenameLocked(old, new string) error {
	if !Valid(new) {
		return ErrIllegalName
	}
	if _, ok := s.names[old]; !ok {
		return errors.New("name: source missing: " + old)
	}
	if _, ok := s.names[new]; ok {
		return errors.New("name: would overwrite: " + new)
	}
	delete(s.names, old)
	s.names[new] = struct{}{}
	return nil
}

// Snapshot 返回所有名字按字典序排序的切片。
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

// Equal 报告两个命名空间是否逐元素相同（顺序无关）。
func (s *Space) Equal(other *Space) bool {
	a, b := s.Snapshot(), other.Snapshot()
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
