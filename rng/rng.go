// Package rng 提供可注入种子的伪随机源。
package rng

import (
	"errors"
	"math/rand/v2"
)

// ErrBadBound 表示 Intn 的上界非正。
var ErrBadBound = errors.New("rng: bound must be positive")

// Source 是确定性的伪随机源，同种子产生同序列。非并发安全。
type Source struct {
	r *rand.Rand
}

// New 以 seed 构造随机源。
func New(seed uint64) *Source {
	return &Source{r: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))}
}

// Intn 返回 [0, n) 内均匀分布的伪随机整数。
func (s *Source) Intn(n int) (int, error) {
	if n <= 0 {
		return 0, ErrBadBound
	}
	return s.r.IntN(n), nil
}
