package store

import "time"

// RealClock 使用真实时钟（UnixNano）。Store 会在其基础上做主键内
// 单调钳制，保证严格递增且不回退。
type RealClock struct{}

// Now 返回当前真实时刻的 Unix 纳秒。
func (RealClock) Now() int64 { return time.Now().UnixNano() }

// FakeClock 是确定性时钟：从 start 起每次调用递增 1。
// 用于测试与可复现重放。非并发安全，由 Store 的链锁串行化调用。
type FakeClock struct {
	next int64
}

// NewFakeClock 创建从 start 开始的确定性时钟。
func NewFakeClock(start int64) *FakeClock { return &FakeClock{next: start} }

// Now 返回当前逻辑时刻并前进一格。
func (c *FakeClock) Now() int64 {
	t := c.next
	c.next++
	return t
}
