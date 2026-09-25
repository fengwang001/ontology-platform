// Package rng 提供可注入种子的伪随机源，仅依赖标准库。
package rng

import (
	"errors"
	"math/rand/v2"
)

// ErrNonPositiveN 表示 Intn 收到了非正的上界。
var ErrNonPositiveN = errors.New("rng: Intn 的上界必须为正整数")

// Source 是确定性的伪随机源，相同种子产生相同序列。
// 每个 Source 独立持有状态，不共享即可并发安全。
type Source struct {
	r *rand.Rand
}

// New 以 seed 构造一个伪随机源。
func New(seed uint64) *Source {
	return &Source{r: rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15))}
}

// Intn 返回 [0, n) 内均匀分布的伪随机整数。
func (s *Source) Intn(n int) (int, error) {
	if n <= 0 {
		return 0, ErrNonPositiveN
	}
	return s.r.IntN(n), nil
}
