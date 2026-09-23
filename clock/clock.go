// Package clock 提供可注入的逻辑时钟，并强制单调前进。
package clock

import "errors"

// ErrClockMovedBack 表示检测到时钟回拨：当前读数小于此前已见读数。
var ErrClockMovedBack = errors.New("clock moved backwards")

// Clock 是可注入的时钟。Now 返回单调非降的逻辑时间（纳秒）。
type Clock interface {
	Now() (int64, error)
}

// Real 用封装的时间源（通常为 time.Now().UnixNano）实现 Clock。
type Real struct {
	read func() int64
	last int64
}

// NewReal 返回基于真实时间源的时钟。
func NewReal(read func() int64) *Real { return &Real{read: read} }

// Now 返回当前时间；若时间源回拨则返回 ErrClockMovedBack。
func (c *Real) Now() (int64, error) {
	t := c.read()
	if t < c.last {
		return 0, ErrClockMovedBack
	}
	c.last = t
	return t, nil
}

// Fake 是测试用手动推进时钟。Advance 只能前进，Set 可用于制造回拨。
type Fake struct {
	t    int64
	last int64
}

// NewFake 返回初始时刻 t 的假时钟。
func NewFake(t int64) *Fake { return &Fake{t: t, last: t} }

// Now 读取当前时刻；回拨（见 Set）时返回 ErrClockMovedBack。
func (c *Fake) Now() (int64, error) {
	if c.t < c.last {
		return 0, ErrClockMovedBack
	}
	c.last = c.t
	return c.t, nil
}

// Advance 将时钟向前拨 d 纳秒（d 必须为非负）。
func (c *Fake) Advance(d int64) { c.t += d }

// Set 直接设置时刻；设置为更早的值后，下一次 Now 报告回拨。
func (c *Fake) Set(t int64) { c.t = t }
