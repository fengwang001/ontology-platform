// Package guard 实现单分区的删除熔断器：比例判定、Suspect 状态与人工放行。
package guard

import "errors"

var ErrNotSuspect = errors.New("guard: partition not suspect")

// Breaker 是单分区的熔断器。不是并发安全的，由调用方串行化。
type Breaker struct {
	x       int64
	suspect bool
}

func NewBreaker(x int) *Breaker {
	return &Breaker{x: int64(x)}
}

// Suspect 报告熔断器是否处于 Suspect 状态。
func (b *Breaker) Suspect() bool {
	return b.suspect
}

// Evaluate 在分区未 Suspect 时判定是否熔断：当 |C|*100 严格大于 X*n0 时
// 置 Suspect 并返回 true（恰等不熔断）。已 Suspect 时不再判定，返回 false。
// |C| 与 n0 的量级保证乘积不超过 1e11，int64 无溢出。
func (b *Breaker) Evaluate(c, n0 int) bool {
	if b.suspect {
		return false
	}
	if int64(c)*100 > b.x*int64(n0) {
		b.suspect = true
		return true
	}
	return false
}

// Clear 在 Suspect 状态下人工放行，清除 Suspect；否则返回 ErrNotSuspect。
func (b *Breaker) Clear() error {
	if !b.suspect {
		return ErrNotSuspect
	}
	b.suspect = false
	return nil
}
