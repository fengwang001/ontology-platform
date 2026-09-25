// Package rng 提供可注入种子的伪随机源，供洗牌等场景复现使用。
package rng

import (
	"errors"
	"math/rand/v2"
)

// ErrNonPositiveN 表示 Intn 收到了非正的上界。
var ErrNonPositiveN = errors.New("rng: n must be positive")

// RNG 是确定性的伪随机源，相同种子产生相同序列。不可并发共享。
type RNG struct {
	r *rand.Rand
}

// New 以 seed 构造随机源。
func New(seed uint64) *RNG {
	return &RNG{r: rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15))}
}

// Intn 返回 [0, n) 内均匀分布的随机数；n <= 0 时返回 ErrNonPositiveN。
func (g *RNG) Intn(n int) (int, error) {
	if n <= 0 {
		return 0, ErrNonPositiveN
	}
	return g.r.IntN(n), nil
}
