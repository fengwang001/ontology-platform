package weightedsample

import (
	"math"
	"sync"
)

// RandomSource 为抽样器提供可注入、可复现的随机数序列。
//
// Peek 返回下一个随机数及其可用性，但不推进游标；只有抽样器确认
// 接受当前元素后才调用 Commit 推进。被拒绝（含校验失败与排名不足）
// 的元素不调用 Commit，游标保持不变。
type RandomSource interface {
	Peek() (float64, bool)
	Commit()
	Consumed() int
}

// Sequence 是由调用方注入确定序列的线程安全随机源。
type Sequence struct {
	mu     sync.Mutex
	values []float64
	index  int
}

// NewSequence 校验并包装一个确定的随机数序列，数值必须位于 [0,1)。
// 空序列是合法的；当抽样器需要取数时才报 ErrRandomExhausted。
func NewSequence(values []float64) (*Sequence, error) {
	for _, value := range values {
		if !validUnitFloat(value) {
			return nil, kindError(ErrInvalidRandom,
				"sequence value at index must be in [0,1)")
		}
	}
	copied := make([]float64, len(values))
	copy(copied, values)
	return &Sequence{values: copied}, nil
}

// validUnitFloat 报告 value 是否为可用于取数的有限数且位于 [0,1)。
func validUnitFloat(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value < 1
}

// Peek 返回下一个随机数但不消耗它。游标不移动，可重复调用。
func (s *Sequence) Peek() (float64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.index >= len(s.values) {
		return 0, false
	}
	return s.values[s.index], true
}

// Commit 消耗 Peek 过的下一个随机数。游标已到底时为无操作，
// 调用方必须仅在 Peek 成功后调用。
func (s *Sequence) Commit() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.index < len(s.values) {
		s.index++
	}
}

// Consumed 返回已消耗的随机数个数。
func (s *Sequence) Consumed() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.index
}
