// Package ddl 实现 v1/v2 双表、三阶段读写分派、双写原子性与幂等回填。
package ddl

import (
	"errors"
	"sync"

	"ontology/phase"
)

// 可判定哨兵错误，互不相同。
var (
	ErrBadPhase       = errors.New("ddl: illegal phase for operation")
	ErrEmptyKey       = errors.New("ddl: empty key")
	ErrBadValue       = errors.New("ddl: value negative or 2*v exceeds maxValue")
	ErrDualWriteFault = errors.New("ddl: injected dual-write fault")
)

// Store 是在线迁移的双表存储，状态全在进程内存。
type Store struct {
	mu          sync.RWMutex
	ph          phase.Phase
	v1, v2      map[string]int
	maxValue    int
	fault       bool
	done        map[string]bool // 已回填/已双写一致的键
	lastChecked int             // 最近一次 Backfill 实际检查/写入的键数（非导出）
}

// New 建一个处于 Normal 阶段的空存储。
func New(maxValue int) *Store {
	return &Store{
		ph:       phase.Normal,
		v1:       map[string]int{},
		v2:       map[string]int{},
		maxValue: maxValue,
		done:     map[string]bool{},
	}
}

// Phase 返回当前阶段（只读）。
func (s *Store) Phase() phase.Phase {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ph
}

// Put 按当前阶段写；任何校验失败都在写入之前返回，状态不变。
func (s *Store) Put(k string, v int) error {
	if k == "" {
		return ErrEmptyKey
	}
	if v < 0 || 2*v > s.maxValue {
		return ErrBadValue
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.ph {
	case phase.Normal:
		s.v1[k] = v
	case phase.DualWrite:
		if s.fault { // v2 一半失败 → 整体失败，v1 也不写
			return ErrDualWriteFault
		}
		s.v1[k] = v
		s.v2[k] = 2 * v
		s.done[k] = true
	case phase.Switched:
		s.v2[k] = 2 * v
	}
	return nil
}

// Get 按当前阶段读；键不存在返回 0。
func (s *Store) Get(k string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.ph == phase.Switched {
		return s.v2[k]
	}
	return s.v1[k]
}

// BeginMigration 推进 Normal → DualWrite。
func (s *Store) BeginMigration() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next, ok := s.ph.Begin()
	if !ok {
		return ErrBadPhase
	}
	s.ph = next
	return nil
}

// Switch 推进 DualWrite → Switched。
func (s *Store) Switch() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next, ok := s.ph.Switch()
	if !ok {
		return ErrBadPhase
	}
	s.ph = next
	return nil
}

// Backfill 仅 DualWrite 合法：对未处理的键令 v2[k]=2*v1[k]，幂等。
func (s *Store) Backfill() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ph != phase.DualWrite {
		return ErrBadPhase
	}
	n := 0
	for k, v := range s.v1 {
		if s.done[k] {
			continue // 已回填/已双写，跳过，不重扫
		}
		s.v2[k] = 2 * v
		s.done[k] = true
		n++
	}
	s.lastChecked = n
	return nil
}

// Snapshot 返回 v1/v2 两表的拷贝，供演示与自检核对（不含任何内部计数）。
func (s *Store) Snapshot() (v1, v2 map[string]int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v1, v2 = map[string]int{}, map[string]int{}
	for k, v := range s.v1 {
		v1[k] = v
	}
	for k, v := range s.v2 {
		v2[k] = v
	}
	return v1, v2
}

// InjectDualWriteFault 注入双写故障（持久，直到进程结束）。
func (s *Store) InjectDualWriteFault() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fault = true
}
