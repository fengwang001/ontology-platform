// Package formulary 维护药品目录与成分日极量，首次被接受的 Submit 后冻结。
package formulary

import (
	"errors"
	"sync"
)

var (
	// ErrParam 表示配置参数非法（含量、极量、等级越界，成分为空或重复登记）。
	ErrParam = errors.New("formulary: 参数非法")
	// ErrFrozen 表示首次 Submit 之后再次调用配置接口。
	ErrFrozen = errors.New("formulary: 已冻结")
)

const (
	maxMg    = 1_000_000
	maxLimit = 1_000_000_000
)

// Drug 为药品目录项：成分、每片含量（毫克）与处方权等级。
type Drug struct {
	ID    string
	Ing   string
	Mg    int64
	Level int
}

// Store 为药品目录与日极量表，并发安全。
type Store struct {
	mu     sync.RWMutex
	drugs  map[string]Drug
	limits map[string]int64
	frozen bool
}

func NewStore() *Store {
	return &Store{drugs: make(map[string]Drug), limits: make(map[string]int64)}
}

// AddDrug 登记药品：成分非空，每片 1..10^6 毫克，处方权等级 1..3。
func (s *Store) AddDrug(id, ing string, mg int64, level int) error {
	if id == "" || ing == "" || mg < 1 || mg > maxMg || level < 1 || level > 3 {
		return ErrParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.frozen {
		return ErrFrozen
	}
	if _, dup := s.drugs[id]; dup {
		return ErrParam
	}
	s.drugs[id] = Drug{ID: id, Ing: ing, Mg: mg, Level: level}
	return nil
}

// SetMax 设成分日极量（毫克，1..10^9）；未设的成分视为无上限。
func (s *Store) SetMax(ing string, maxDay int64) error {
	if ing == "" || maxDay < 1 || maxDay > maxLimit {
		return ErrParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.frozen {
		return ErrFrozen
	}
	s.limits[ing] = maxDay
	return nil
}

// Freeze 冻结配置，由 review 引擎在首次被接受的 Submit 时调用。
func (s *Store) Freeze() {
	s.mu.Lock()
	s.frozen = true
	s.mu.Unlock()
}

// Drug 按 id 查药品。
func (s *Store) Drug(id string) (Drug, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.drugs[id]
	return d, ok
}

// MaxDay 查成分日极量；第二个返回值为 false 表示无上限。
func (s *Store) MaxDay(ing string) (int64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.limits[ing]
	return m, ok
}
