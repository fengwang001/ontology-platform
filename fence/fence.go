// Package fence 提供围栏令牌（fencing token）的分配与校验。
//
// 令牌是单调递增的 uint64。每次授予（Grant）产生一个严格大于
// 以往所有令牌的新令牌；校验（Accept）维护"已接受的最大令牌"
// 水位，任何小于水位的令牌都会被拒绝，水位只升不降。
package fence

import "sync"

// Token 是围栏令牌，0 为未分配时的零值，真实令牌从 1 开始。
type Token uint64

// Fence 是单个资源的围栏令牌分配器与校验器，并发安全。
type Fence struct {
	mu      sync.Mutex
	granted Token // 最近一次授予的令牌
	high    Token // 水位：已授予或已接受的最大令牌
}

// New 返回一个初始水位为 0 的 Fence。
func New() *Fence {
	return &Fence{}
}

// Grant 分配一个新令牌，严格大于此前分配过的所有令牌，
// 并把水位抬升到该令牌。
func (f *Fence) Grant() Token {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.granted++
	if f.granted > f.high {
		f.high = f.granted
	}
	return f.granted
}

// Accept 校验一个写入令牌：不小于当前水位则接受并把水位抬升到
// 该令牌；否则返回 *StaleError（可用 errors.Is(err, ErrStale) 判定）。
func (f *Fence) Accept(t Token) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t < f.high {
		return &StaleError{Token: t, Watermark: f.high}
	}
	f.high = t
	return nil
}

// Current 返回最近一次授予的令牌，未授予过时为 0。
func (f *Fence) Current() Token {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.granted
}

// Watermark 返回当前水位（已授予或已接受的最大令牌）。
func (f *Fence) Watermark() Token {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.high
}
