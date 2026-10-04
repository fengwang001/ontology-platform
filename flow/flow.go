// Package flow 保存工作流定义：上限掩码与逐步需求掩码。
package flow

import (
	"errors"
	"sync"

	"ontology/grants"
)

// MaxSteps 是每个工作流定义允许的最大步骤数。
const MaxSteps = 16

// ErrDef 表示定义非法：名称为空、ceil 含位 63、步骤数越界、
// 需求掩码为零或不是 ceil 的子集。属参数类错误。
var ErrDef = errors.New("flow: invalid definition")

// Def 是一条工作流定义。
type Def struct {
	Ceil uint64
	Reqs []uint64
}

// Store 保存名称到定义的映射。并发安全。
type Store struct {
	mu   sync.RWMutex
	defs map[string]Def
}

// New 返回空的定义存储。
func New() *Store {
	return &Store{defs: make(map[string]Def)}
}

// Define 校验并保存定义；重复定义同名工作流会覆盖。
func (s *Store) Define(name string, ceil uint64, reqs []uint64) error {
	if name == "" || ceil&grants.ApproveBit != 0 || len(reqs) == 0 || len(reqs) > MaxSteps {
		return ErrDef
	}
	for _, req := range reqs {
		if req == 0 || req&^ceil != 0 {
			return ErrDef
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.defs[name] = Def{Ceil: ceil, Reqs: append([]uint64(nil), reqs...)}
	return nil
}

// Get 按名称取定义。
func (s *Store) Get(name string) (Def, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.defs[name]
	return d, ok
}
