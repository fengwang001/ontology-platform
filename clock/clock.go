// Package clock 提供可注入的整数刻时钟，不依赖其他包。
package clock

import (
	"errors"
	"sync"
)

// ErrClockBackward 表示时钟被要求向更早的时刻推进。
var ErrClockBackward = errors.New("clock: cannot advance to an earlier tick")

// Clock 是并发安全的整数刻时钟，时间只能单调非递减。
type Clock struct {
	mu  sync.Mutex
	now int64
}

// New 创建起始刻为 start 的时钟。
func New(start int64) *Clock {
	return &Clock{now: start}
}

// Now 返回当前时刻。
func (c *Clock) Now() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance 将时钟向前推进 delta 刻；delta 为负返回 ErrClockBackward 且状态不变。
func (c *Clock) Advance(delta int64) (int64, error) {
	if delta < 0 {
		return c.Now(), ErrClockBackward
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now += delta
	return c.now, nil
}
