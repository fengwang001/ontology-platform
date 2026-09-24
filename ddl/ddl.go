// Package ddl 维护 v1/v2 双表，按阶段分派读写，保证双写原子与回填幂等。
package ddl

import (
	"errors"
	"sync"

	"ontology/phase"
)

// 四类可判定哨兵错误，互不相同。
var (
	ErrPhase     = errors.New("ddl: illegal phase for operation")
	ErrKey       = errors.New("ddl: empty key")
	ErrValue     = errors.New("ddl: value out of range")
	ErrDualWrite = errors.New("ddl: dual-write failure")
)

// Store 是进程内存中的双表存储。所有被拒绝的操作在任何写之前返回错误。
type Store struct {
	mu          sync.RWMutex
	ph          phase.Phase
	v1, v2      map[string]int
	maxValue    int
	fault       bool // 注入的双写故障：DualWrite 的 Put 其 v2 一半失败
	backfilled  map[string]struct{}
	lastChecked int // 最近一次 Backfill 实际检查/写入的 key 个数（非导出，不外泄）
}

func New(maxValue int) *Store {
	return &Store{
		v1:         make(map[string]int),
		v2:         make(map[string]int),
		maxValue:   maxValue,
		backfilled: make(map[string]struct{}),
	}
}

// Put 按当前阶段写。校验失败或双写故障时不改变任何状态。
func (s *Store) Put(k string, v int) error {
	if k == "" {
		return ErrKey
	}
	if v < 0 || 2*v > s.maxValue {
		return ErrValue
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.ph {
	case phase.Normal:
		s.v1[k] = v
	case phase.DualWrite:
		if s.fault { // v2 一半失败 → 整次 Put 失败，v1 也不写
			return ErrDualWrite
		}
		s.v1[k] = v // 同一临界区内写两表，原子成立
		s.v2[k] = 2 * v
		s.backfilled[k] = struct{}{}
	case phase.Switched:
		s.v2[k] = 2 * v // v1 冻结
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

// BeginMigration：Normal → DualWrite。
func (s *Store) BeginMigration() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ph != phase.Normal {
		return ErrPhase
	}
	next, ok := s.ph.Next()
	if !ok {
		return ErrPhase
	}
	s.ph = next
	return nil
}

// Backfill 仅 DualWrite 合法；幂等：恒写 v2[k]=2*v1[k]，已处理键靠标记跳过。
func (s *Store) Backfill() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ph != phase.DualWrite {
		return ErrPhase
	}
	n := 0
	for k, v := range s.v1 {
		if _, done := s.backfilled[k]; done {
			continue
		}
		s.v2[k] = 2 * v
		s.backfilled[k] = struct{}{}
		n++
	}
	s.lastChecked = n
	return nil
}

// Switch：DualWrite → Switched。
func (s *Store) Switch() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ph != phase.DualWrite {
		return ErrPhase
	}
	next, ok := s.ph.Next()
	if !ok {
		return ErrPhase
	}
	s.ph = next
	return nil
}

// InjectDualWriteFault 注入双写故障（单向，用于测试）。
func (s *Store) InjectDualWriteFault() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fault = true
}

// Snapshot 返回当前阶段与两表副本，供观测与测试。
func (s *Store) Snapshot() (phase.Phase, map[string]int, map[string]int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a := make(map[string]int, len(s.v1))
	for k, v := range s.v1 {
		a[k] = v
	}
	b := make(map[string]int, len(s.v2))
	for k, v := range s.v2 {
		b[k] = v
	}
	return s.ph, a, b
}
