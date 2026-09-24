// Package rnd 提供注入式确定随机源：一串固定的 u，按顺序逐个取用。
// 它不知道蓄水池与权重的存在，也不依赖本工程其他任何包。
// Source 本身不加锁；并发安全由上层（api 包）串行化保证。
package rnd

import "errors"

// ErrExhausted 是“随机源已用尽仍要取数”的可判定哨兵错误。
var ErrExhausted = errors.New("rnd: random source exhausted")

// Source 是有限、有序、只进的随机数序列。
type Source struct {
	us  []float64
	pos int
}

// New 用给定序列构造随机源（复制切片，避免调用方后续篡改）。
// 合法性（每个 u 满足 0<u<1）由 api.New 统一校验。
func New(us []float64) *Source {
	cp := make([]float64, len(us))
	copy(cp, us)
	return &Source{us: cp}
}

// ValidU 报告 u 是否满足 0 < u < 1。
func ValidU(u float64) bool { return u > 0 && u < 1 }

// Remaining 返回尚未消耗的随机数个数。
func (s *Source) Remaining() int { return len(s.us) - s.pos }

// Consumed 返回已经消耗的随机数个数。
func (s *Source) Consumed() int { return s.pos }

// Take 消耗并返回序列中的下一个 u；游标恰好前进一格。
// 已用尽时返回 ErrExhausted 且不改变游标。
func (s *Source) Take() (float64, error) {
	if s.pos >= len(s.us) {
		return 0, ErrExhausted
	}
	u := s.us[s.pos]
	s.pos++
	return u, nil
}

// At 返回下标 i 处的随机数（不自增游标），供自检核对键值。
func (s *Source) At(i int) float64 { return s.us[i] }
