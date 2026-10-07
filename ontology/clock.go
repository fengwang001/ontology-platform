package ontology

import "time"

// Clock 是租约失效裁定所用的唯一时间依据。系统内所有“占用是否仍有效”
// 的判定都只引用同一 Clock 的读数，因此在持有方是否真正中止尚不能确定
// （租约尚未到期）之前，占用权绝不会被提前转交。
type Clock interface {
	Now() int64
}

// WallClock 使用单调性质由调用方保证的墙上时间（毫秒）。
type WallClock struct{}

func (WallClock) Now() int64 { return time.Now().UnixMilli() }

// FakeClock 是供测试与重放使用的手动推进时钟。
type FakeClock struct{ T int64 }

func (c *FakeClock) Now() int64 { return c.T }

// Advance 把时钟向前拨 d 毫秒并返回新读数。
func (c *FakeClock) Advance(d int64) int64 { c.T += d; return c.T }
