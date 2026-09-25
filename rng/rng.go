// Package rng 提供可注入种子的确定性伪随机源。
package rng

import (
	"errors"
	"math/rand/v2"
)

// ErrInvalidBound 在 Intn 收到非正上界时返回。
var ErrInvalidBound = errors.New("rng: bound must be positive")

// RNG 是无锁、无共享状态的伪随机源：每个调用方持有自己的实例即可安全并发。
type RNG struct {
	pc    *rand.PCG
	calls int
}

// New 用单个种子构造随机源，同种子的实例产生完全相同的序列。
func New(seed uint64) *RNG {
	return &RNG{pc: rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)}
}

// Intn 返回 [0, n) 内的伪随机整数，并把调用计数加一。
func (r *RNG) Intn(n int) (int, error) {
	if n <= 0 {
		return 0, ErrInvalidBound
	}
	r.calls++
	bound := uint64(n)
	threshold := (^bound + 1) % bound // 2^64 mod bound：拒绝区上界，消除取模偏差
	value := r.pc.Uint64()
	for value < threshold {
		value = r.pc.Uint64()
	}
	return int(value % bound), nil
}

// Calls 返回该实例上 Intn 的调用次数。
func (r *RNG) Calls() int {
	return r.calls
}
