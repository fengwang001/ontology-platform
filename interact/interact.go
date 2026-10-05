// Package interact 维护成分相互作用表与患者过敏表，首次被接受的 Submit 后冻结。
package interact

import (
	"errors"
	"sync"
	"sync/atomic"
)

var (
	// ErrParam 表示配置参数非法（成分为空、两成分相同或等级越界）。
	ErrParam = errors.New("interact: 参数非法")
	// ErrFrozen 表示首次 Submit 之后再次调用配置接口。
	ErrFrozen = errors.New("interact: 已冻结")
)

// Store 为相互作用与过敏表，并发安全；probes 统计相互作用表查询次数。
type Store struct {
	mu        sync.RWMutex
	grades    map[[2]string]int
	allergies map[string]map[string]bool
	frozen    bool
	probes    atomic.Int64
}

func NewStore() *Store {
	return &Store{
		grades:    make(map[[2]string]int),
		allergies: make(map[string]map[string]bool),
	}
}

func pairKey(a, b string) [2]string {
	if a > b {
		a, b = b, a
	}
	return [2]string{a, b}
}

// SetPair 设两个不同成分的相互作用等级：1 提示、2 慎用、3 禁忌；对称，未设为 0。
func (s *Store) SetPair(ingA, ingB string, grade int) error {
	if ingA == "" || ingB == "" || ingA == ingB || grade < 1 || grade > 3 {
		return ErrParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.frozen {
		return ErrFrozen
	}
	s.grades[pairKey(ingA, ingB)] = grade
	return nil
}

// SetAllergy 登记患者对某成分过敏。
func (s *Store) SetAllergy(patient, ing string) error {
	if patient == "" || ing == "" {
		return ErrParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.frozen {
		return ErrFrozen
	}
	m := s.allergies[patient]
	if m == nil {
		m = make(map[string]bool)
		s.allergies[patient] = m
	}
	m[ing] = true
	return nil
}

// Freeze 冻结配置，由 review 引擎在首次被接受的 Submit 时调用。
func (s *Store) Freeze() {
	s.mu.Lock()
	s.frozen = true
	s.mu.Unlock()
}

// Grade 查两成分相互作用等级（0 表示未设）；每次调用计一次 probe。
func (s *Store) Grade(a, b string) int {
	s.probes.Add(1)
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.grades[pairKey(a, b)]
}

// Allergic 判断患者是否对某成分过敏。
func (s *Store) Allergic(patient, ing string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.allergies[patient][ing]
}

// Probes 返回相互作用表累计查询次数。
func (s *Store) Probes() int64 { return s.probes.Load() }

// ResetProbes 清零 probes 计数器（测试用）。
func (s *Store) ResetProbes() { s.probes.Store(0) }
