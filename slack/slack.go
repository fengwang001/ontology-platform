// Package slack 实现单 Key 的 K-slack 容忍窗口判定：是否超窗、是否推进高水位。
// 纯函数，不保存任何状态，不依赖其他包。
package slack

// Verdict 是对一条事件在给定高水位下的判定结果。
type Verdict int

const (
	// First 该 Key 的首个事件：无条件接受，high 由"无"设为 Seq。
	First Verdict = iota
	// Advance Seq > high：接受，并推进 high = Seq。
	Advance
	// InWindow high-K <= Seq <= high：乱序但不算太旧，接受但不推进 high。
	InWindow
	// Dropped Seq < high-K：超窗丢弃，high 不变。
	Dropped
)

// Judge 判定 seq 在容忍窗口 [high-K, high]（左闭右闭）下的结果。
// hasHigh 为 false 表示该 Key 尚无高水位（"无"，不是 0）。
// K 必须 >= 0。
func Judge(K int64, high int64, hasHigh bool, seq int64) Verdict {
	if !hasHigh {
		return First
	}
	switch {
	case seq > high:
		return Advance
	case seq >= high-K:
		return InWindow
	default:
		return Dropped
	}
}

// Apply 根据判定结果计算新的高水位：只有 First 和 Advance 会推进 high，
// InWindow 与 Dropped 都保持 high 不变（高水位只进不退，丢弃不留痕）。
func Apply(v Verdict, high int64, seq int64) int64 {
	if v == First || v == Advance {
		return seq
	}
	return high
}

// Accepted 报告该判定是否计入接受数（仅 Dropped 不计）。
func Accepted(v Verdict) bool {
	return v != Dropped
}
