// Package thr 提供跨阈值报警的纯函数判定：边沿触发 + 迟滞去抖。
package thr

// Event 是报警事件类型。
type Event int

const (
	None Event = iota // 不发事件
	On                // 报警 ON
	Off               // 报警 OFF
)

// Judge 给定旧值、新值与旧 on 状态，返回新 on 状态、是否发事件及事件类型。
// 规则：OFF 且 newVal >= T 发 ON；ON 且 newVal < T-H 发 OFF；其余不变。
// 恰好等于 T 触发 ON；恰好等于 T-H 保持 ON。
func Judge(oldVal, newVal int64, oldOn bool, t, h int64) (newOn bool, emit bool, ev Event) {
	_ = oldVal // 判定只依赖新值与旧状态，旧值仅为签名完整性保留
	switch {
	case !oldOn && newVal >= t:
		return true, true, On
	case oldOn && newVal < t-h:
		return false, true, Off
	default:
		return oldOn, false, None
	}
}
