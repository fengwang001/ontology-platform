// Package wmline 实现单条水位线：maxSeen、wm、delay 与迟到判定。不依赖其他包。
package wmline

import "math"

// NegInf 表示负无穷：尚无任何事件时 maxSeen 与 wm 的取值。
const NegInf = int64(math.MinInt64)

// Line 是单条水位线。用 New 构造。
type Line struct {
	maxSeen  int64
	wm       int64
	delay    int64
	minDelay int64
	maxDelay int64
	seen     bool
}

// New 构造水位线，delay 初值为 minDelay。调用方保证 0 <= minDelay <= maxDelay。
func New(minDelay, maxDelay int64) *Line {
	return &Line{wm: NegInf, delay: minDelay, minDelay: minDelay, maxDelay: maxDelay}
}

// Observe 处理一个事件：先用本条之前的 wm 判定迟到（wm 为负无穷或 TS==wm 都不算迟到），
// 再更新 maxSeen，最后用当前 delay 重算 wm。返回是否迟到。
func (l *Line) Observe(ts int64) (late bool) {
	late = l.seen && ts < l.wm
	if !l.seen || ts > l.maxSeen {
		l.maxSeen = ts
	}
	l.seen = true
	l.wm = l.maxSeen - l.delay
	return late
}

// SetDelay 调整滞后量并钳制在 [minDelay, maxDelay]。
// 注意：按规则 wm 只在事件到达时重算，这里绝不动 wm。
func (l *Line) SetDelay(d int64) {
	if d < l.minDelay {
		d = l.minDelay
	}
	if d > l.maxDelay {
		d = l.maxDelay
	}
	l.delay = d
}

// WM 返回当前水位线；无任何事件时为 NegInf。
func (l *Line) WM() int64 { return l.wm }

// Delay 返回当前滞后量。
func (l *Line) Delay() int64 { return l.delay }
