// Package store 提供版本化对象存储：版本记录、对象锁与整键原子应用。
package store

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// MaxClock 是创建时刻 c 的上界（含）。
const MaxClock = int64(1_000_000_000_000)

var (
	ErrInvalid      = errors.New("store: invalid argument")
	ErrNotFound     = errors.New("store: version not found")
	ErrRemoveFailed = errors.New("store: remove failed")
)

// Version 是一条版本记录。R 为保留截止（0 表示无锁），删除标记恒为 0。
type Version struct {
	Ver    uint64
	C      int64
	Marker bool
	R      int64
}

// Store 是并发安全的版本化对象存储。键为非空字节串，版本按 Ver 升序保存。
type Store struct {
	mu     sync.Mutex
	keys   map[string][]Version
	maxVer uint64 // 全局序号高水位（含已回滚的），只增不减
	hook   func(key string, ver uint64) bool
}

func New() *Store { return &Store{keys: make(map[string][]Version)} }

// SetRemoveHook 注入 Remove 失败点：hook 返回 true 时该次移除失败。
// 传 nil 撤销注入。
func (s *Store) SetRemoveHook(h func(key string, ver uint64) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hook = h
}

// Load 直接装载一个版本。要求键非空、c 在 [0, MaxClock]、R 非负、标记无锁、
// 同键内 Ver 严格递增且 C 随版本号不减。
func (s *Store) Load(key string, v Version) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key == "" {
		return fmt.Errorf("%w: empty key", ErrInvalid)
	}
	if v.C < 0 || v.C > MaxClock {
		return fmt.Errorf("%w: c %d out of range", ErrInvalid, v.C)
	}
	if v.R < 0 {
		return fmt.Errorf("%w: negative retain-until %d", ErrInvalid, v.R)
	}
	if v.Marker && v.R != 0 {
		return fmt.Errorf("%w: delete marker with retain-until %d", ErrInvalid, v.R)
	}
	e := s.keys[key]
	if len(e) > 0 {
		last := e[len(e)-1]
		if v.Ver <= last.Ver {
			return fmt.Errorf("%w: version %d not greater than %d", ErrInvalid, v.Ver, last.Ver)
		}
		if v.C < last.C {
			return fmt.Errorf("%w: c %d decreases from %d", ErrInvalid, v.C, last.C)
		}
	}
	s.keys[key] = append(e, v)
	if v.Ver > s.maxVer {
		s.maxVer = v.Ver
	}
	return nil
}

// Remove 删除指定版本，可被注入失败。
func (s *Store) Remove(key string, ver uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hook != nil && s.hook(key, ver) {
		return fmt.Errorf("store: remove %q v%d: %w", key, ver, ErrRemoveFailed)
	}
	if _, ok := s.deleteLocked(key, ver); !ok {
		return fmt.Errorf("store: remove %q v%d: %w", key, ver, ErrNotFound)
	}
	return nil
}

// Apply 对单个键原子地应用一组改动：先追加删除标记（版本号取全局序号
// 高水位加一），再按序移除 removes；dropAdded 为真时把本次追加的标记也
// 一并移除（用于孤儿标记清理）。任一步失败则整键回滚并返回错误。
func (s *Store) Apply(key string, markers []int64, removes []uint64, dropAdded bool) ([]Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.keys[key]
	if !ok {
		return nil, fmt.Errorf("store: apply %q: %w", key, ErrNotFound)
	}
	var added []Version
	for _, c := range markers {
		s.maxVer++
		v := Version{Ver: s.maxVer, C: c, Marker: true}
		e = append(e, v)
		added = append(added, v)
	}
	s.keys[key] = e
	queue := append([]uint64(nil), removes...)
	if dropAdded {
		for _, a := range added {
			queue = append(queue, a.Ver)
		}
	}
	var done []Version
	for _, ver := range queue {
		if s.hook != nil && s.hook(key, ver) {
			s.rollbackLocked(key, added, done)
			return nil, fmt.Errorf("store: remove %q v%d: %w", key, ver, ErrRemoveFailed)
		}
		v, ok := s.deleteLocked(key, ver)
		if !ok {
			s.rollbackLocked(key, added, done)
			return nil, fmt.Errorf("store: remove %q v%d: %w", key, ver, ErrNotFound)
		}
		done = append(done, v)
	}
	return added, nil
}

// rollbackLocked 撤销整键改动：先删掉仍在的追加标记，再恢复已移除版本。
func (s *Store) rollbackLocked(key string, added, done []Version) {
	for _, av := range added {
		s.deleteLocked(key, av.Ver)
	}
	for _, rv := range done {
		s.restoreLocked(key, rv)
	}
}

func (s *Store) deleteLocked(key string, ver uint64) (Version, bool) {
	e := s.keys[key]
	for i, v := range e {
		if v.Ver == ver {
			e = append(e[:i], e[i+1:]...)
			if len(e) == 0 {
				delete(s.keys, key)
			} else {
				s.keys[key] = e
			}
			return v, true
		}
	}
	return Version{}, false
}

func (s *Store) restoreLocked(key string, v Version) {
	e := s.keys[key]
	i := sort.Search(len(e), func(i int) bool { return e[i].Ver >= v.Ver })
	e = append(e, Version{})
	copy(e[i+1:], e[i:])
	e[i] = v
	s.keys[key] = e
}

// Versions 返回键的版本记录副本（按 Ver 升序）。
func (s *Store) Versions(key string) []Version {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Version(nil), s.keys[key]...)
}

// Keys 返回全部键的升序快照。
func (s *Store) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.keys))
	for k := range s.keys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Snapshot 返回整个存储的深拷贝，用于最终状态比对。
func (s *Store) Snapshot() map[string][]Version {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string][]Version, len(s.keys))
	for k, e := range s.keys {
		out[k] = append([]Version(nil), e...)
	}
	return out
}
