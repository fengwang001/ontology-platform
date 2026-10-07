package cardinality

import "time"

// Clock 提供单调递增的纳秒时间读数。管理器的全部"当前时刻"判断
// （租约截止、过期回收）都只经由该接口取得，生产环境用墙钟单调读数，
// 测试中注入 FakeClock 以确定性地推进时间、重放瞬时窗口。
type Clock interface {
	NowNanos() int64
}

// SystemClock 基于进程启动后经过的单调时间，避免墙钟回拨影响租约判定。
type SystemClock struct {
	start time.Time
}

func NewSystemClock() *SystemClock { return &SystemClock{start: time.Now()} }

func (c *SystemClock) NowNanos() int64 {
	return time.Since(c.start).Nanoseconds()
}

// FakeClock 是测试用可手动推进的单调时钟，读数只增不减。
type FakeClock struct {
	nanos int64
}

func NewFakeClock(start int64) *FakeClock { return &FakeClock{nanos: start} }

func (c *FakeClock) NowNanos() int64 { return c.nanos }

func (c *FakeClock) Advance(delta time.Duration) { c.nanos += delta.Nanoseconds() }

func (c *FakeClock) Set(nanos int64) {
	if nanos > c.nanos {
		c.nanos = nanos
	}
}
