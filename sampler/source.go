package sampler

import (
	"math"
	"sync"
)

// RandomSource 是调用方注入的随机数序列。
// Next 返回下一个随机数；序列用尽时返回 ErrRandomExhausted。
type RandomSource interface {
	Next() (float64, error)
}

// SeqSource 是一个由确定序列支撑、可并发安全使用的随机源。
type SeqSource struct {
	mu       sync.Mutex
	values   []float64
	consumed int
}

// NewSeqSource 用给定序列构造随机源；序列中任何非法随机数都会导致整体拒绝。
func NewSeqSource(values []float64) (*SeqSource, error) {
	copied := make([]float64, len(values))
	for i, u := range values {
		if !validRandom(u) {
			return nil, ErrInvalidRandom
		}
		copied[i] = u
	}
	return &SeqSource{values: copied}, nil
}

// Next 返回序列中的下一个随机数；用尽返回 ErrRandomExhausted。
func (s *SeqSource) Next() (float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.consumed >= len(s.values) {
		return 0, ErrRandomExhausted
	}
	u := s.values[s.consumed]
	s.consumed++
	return u, nil
}

// Consumed 返回已被成功取走的随机数个数。
func (s *SeqSource) Consumed() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.consumed
}

// Len 返回序列长度。
func (s *SeqSource) Len() int {
	return len(s.values)
}

// validRandom 判定 u 是否为合法随机数：必须严格位于 (0,1) 且非 NaN。
func validRandom(u float64) bool {
	return !math.IsNaN(u) && u > 0 && u < 1
}
