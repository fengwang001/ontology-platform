// Package rng 提供可注入种子的伪随机源（splitmix64）。
// 单个 RNG 实例不可并发使用；不同实例状态互不影响。
package rng

import "errors"

// ErrBadBound 表示 Intn 的上界非法（n <= 0）。
var ErrBadBound = errors.New("rng: bound must be positive")

// RNG 是 splitmix64 伪随机源，状态仅 8 字节，由种子完全决定。
type RNG struct {
	state uint64
}

// New 以 seed 构造随机源；同 seed 产生的序列完全相同。
func New(seed uint64) *RNG {
	return &RNG{state: seed}
}

// next 推进状态并返回下一个 64 位伪随机数。
func (r *RNG) next() uint64 {
	r.state += 0x9E3779B97F4A7C15
	z := r.state
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// Intn 返回 [0, n) 内均匀分布的伪随机整数；n <= 0 时返回 ErrBadBound。
func (r *RNG) Intn(n int) (int, error) {
	if n <= 0 {
		return 0, ErrBadBound
	}
	return int(r.next() % uint64(n)), nil
}
