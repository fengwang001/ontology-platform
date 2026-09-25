// Package wtm 提供水位推进、空闲判定与迟到判定的纯函数，不依赖其他包。
package wtm

// Late 判定事件是否迟到：ts-delay 严格小于当前水位才算迟到，等号边界被接受。
func Late(ts, delay, wm int64) bool {
	return ts-delay < wm
}

// Idle 判定流是否空闲：仅看处理时间间隔 pt-lastEventPT 是否达到 timeout。
// hasEvent 为 false 表示尚未有任何事件到达（lastEventPT 视为负无穷），此时必空闲。
func Idle(pt, lastEventPT, timeout int64, hasEvent bool) bool {
	if !hasEvent {
		return true
	}
	return pt-lastEventPT >= timeout
}

// Advance 用候选值推进水位：只进不退，返回 max(wm, cand)。
func Advance(wm, cand int64) int64 {
	if cand > wm {
		return cand
	}
	return wm
}
