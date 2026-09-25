// Package ver 保存记录 (group,key)->{Value,Version}，维护 key 的分组绑定，
// 并增量维护 stamp(group) 与 totalStamp：每次成功 Write 恰使一条记录
// Version+1，故对应组戳与总戳也各 +1，绝不回退。
package ver

import (
	"errors"
	"sync"
)

// 哨兵错误：三类拒绝互不相同，可用 errors.Is 判定。
var (
	ErrEmptyGroup    = errors.New("ver: group is empty")
	ErrEmptyKey      = errors.New("ver: key is empty")
	ErrGroupConflict = errors.New("ver: key is already bound to another group")
)

type rec struct {
	value   int64
	version int64
}

// Store 是进程内、并发安全的记录存储。
type Store struct {
	mu      sync.RWMutex
	groups  map[string]map[string]rec // group -> key -> record
	binding map[string]string         // key -> 永久绑定的 group
	stamps  map[string]int64          // group -> Σ 组内记录 Version
	total   int64                     // Σ 全部记录 Version
}

func New() *Store {
	return &Store{
		groups:  map[string]map[string]rec{},
		binding: map[string]string{},
		stamps:  map[string]int64{},
	}
}

// Write 使 (group,key) 的 Version+1、Value=val。
// 所有校验在任何状态变更之前完成：被拒操作不留痕。
func (s *Store) Write(group, key string, val int64) error {
	if group == "" {
		return ErrEmptyGroup
	}
	if key == "" {
		return ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if g, ok := s.binding[key]; ok && g != group {
		return ErrGroupConflict
	}
	m := s.groups[group]
	if m == nil {
		m = map[string]rec{}
		s.groups[group] = m
		s.stamps[group] = 0
	}
	r := m[key]
	r.value = val
	r.version++
	m[key] = r
	s.stamps[group]++
	s.total++
	if _, ok := s.binding[key]; !ok {
		s.binding[key] = group
	}
	return nil
}

// Stamp 返回 stamp(group)，O(1)。
func (s *Store) Stamp(group string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stamps[group]
}

// TotalStamp 返回 totalStamp，O(1)。
func (s *Store) TotalStamp() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.total
}

// SumGroup 全量重算某组 Value 之和（供缓存失效时调用）。
func (s *Store) SumGroup(group string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var sum int64
	for _, r := range s.groups[group] {
		sum += r.value
	}
	return sum
}

// SumTotal 全量重算全部 Value 之和（供缓存失效时调用）。
func (s *Store) SumTotal() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var sum int64
	for _, m := range s.groups {
		for _, r := range m {
			sum += r.value
		}
	}
	return sum
}

// Groups 返回当前所有非空组名。
func (s *Store) Groups() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.groups))
	for g := range s.groups {
		out = append(out, g)
	}
	return out
}

// Snapshot 返回 group -> key -> Value 的深拷贝，供串行批量重算比对。
func (s *Store) Snapshot() map[string]map[string]int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]map[string]int64, len(s.groups))
	for g, m := range s.groups {
		ng := make(map[string]int64, len(m))
		for k, r := range m {
			ng[k] = r.value
		}
		out[g] = ng
	}
	return out
}
