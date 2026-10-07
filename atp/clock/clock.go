// Package clock 提供系统使用的单调时钟。
//
// 时钟只被“被接受的操作”推进；被拒绝的操作不得改变时钟。
// 时间为非负整数秒。
package clock

import "fmt"

// Time 为非负整数秒。
type Time int64

// Clock 单调时钟：只前进，不后退。
type Clock struct {
	now Time
}

// Now 返回当前时刻。
func (c *Clock) Now() Time { return c.now }

// Accept 尝试把时钟推进到 t。t 小于当前时刻时拒绝（时钟回退），
// 时钟保持不变；否则接受并推进。
func (c *Clock) Accept(t Time) bool {
	if t < c.now {
		return false
	}
	c.now = t
	return true
}

func (t Time) String() string { return fmt.Sprintf("%d", int64(t)) }
