// Package thr 提供跨阈值报警的纯函数判定：边沿触发 + 迟滞去抖。
package thr

// Kind 是事件类型。
type Kind int

const (
	KindNone Kind = iota // 不发事件
	KindOn               // 报警 ON
	KindOff              // 报警 OFF
)

func (k Kind) String() string {
	switch k {
	case KindOn:
		return "ON"
	case KindOff:
		return "OFF"
	default:
		return "NONE"
	}
}

// Judge 给定旧 value、新 value 与旧 on，按规则返回新 on 与应发事件。
// 判定只看状态翻转，与旧 value 无关（oldV 仅为签名完备而保留）：
//   - 当前 OFF 且 newV >= t：转 ON，发 KindOn。
//   - 当前 ON 且 newV < t-h：转 OFF，发 KindOff（newV == t-h 保持 ON）。
//   - 其余：状态不变，发 KindNone。
func Judge(oldV, newV int64, oldOn bool, t, h int64) (newOn bool, kind Kind) {
	_ = oldV
	switch {
	case !oldOn && newV >= t:
		return true, KindOn
	case oldOn && newV < t-h:
		return false, KindOff
	default:
		return oldOn, KindNone
	}
}
