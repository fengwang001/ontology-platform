package fence

import (
	"sync"
)

// Token 是单调递增的围栏令牌。
type Token uint64

// Allocator 为单个资源分配严格单调递增的围栏令牌，
// 并维护「已接受写入」的水位（high-water mark）。
type Allocator struct {
	mu   sync.Mutex
	last Token
	mark Token
}

// NewAllocator 创建分配器，起始令牌为 0（不代表任何持有者）。
func NewAllocator() *Allocator {
	return &Allocator{}
}

// Issue 分配一个严格大于历史所有已分配值的令牌。
func (a *Allocator) Issue() Token {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.last++
	return a.last
}

// Last 返回最近一次授予的令牌；从未授予时为 0。
func (a *Allocator) Last() Token {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.last
}

// Observe 以令牌 tk 校验写入水位：仅当 tk 不小于当前已接受的
// 最大令牌时通过，并把水位抬升到 tk；否则判定为旧令牌写入。
func (a *Allocator) Observe(tk Token) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if tk < a.mark {
		return false
	}
	a.mark = tk
	return true
}

// Mark 返回当前已接受写入的最大令牌。
func (a *Allocator) Mark() Token {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.mark
}
