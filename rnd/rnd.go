// Package rnd 提供注入式随机源：调用方给定确定序列，按序逐个取用。
package rnd

import "errors"

var (
	// ErrExhausted 随机源用尽：需要取数时游标已到末尾。
	ErrExhausted = errors.New("rnd: random source exhausted")
	// ErrBadValue 序列中存在不在 (0,1) 内的值。
	ErrBadValue = errors.New("rnd: value outside (0,1)")
)

// Source 是确定序列上的游标，非并发安全（由上层串行化）。
type Source struct {
	us  []float64
	pos int
}

// New 校验每个 u 都在 (0,1) 内，任一不合法返回 ErrBadValue。
func New(us []float64) (*Source, error) {
	for _, u := range us {
		if !(u > 0 && u < 1) { // NaN 在此判定下也不合法
			return nil, ErrBadValue
		}
	}
	return &Source{us: append([]float64(nil), us...)}, nil
}

// Next 取下一个 u；游标到末尾返回 ErrExhausted 且不移动游标。
func (s *Source) Next() (float64, error) {
	if s.pos >= len(s.us) {
		return 0, ErrExhausted
	}
	u := s.us[s.pos]
	s.pos++
	return u, nil
}

// Consumed 返回已消耗的随机数个数。
func (s *Source) Consumed() int { return s.pos }

// Remaining 返回尚未消耗的随机数个数。
func (s *Source) Remaining() int { return len(s.us) - s.pos }
