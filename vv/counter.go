package vv

import "sync/atomic"

// Counter 是线程安全的比较/丢弃次数计数器，用于复杂度与语义断言。
type Counter struct {
	n atomic.Uint64
}

// Add 累加 n 次。
func (c *Counter) Add(n uint64) { c.n.Add(n) }

// Value 返回当前累计值。
func (c *Counter) Value() uint64 { return c.n.Load() }

// Reset 归零。
func (c *Counter) Reset() { c.n.Store(0) }
