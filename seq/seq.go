// Package seq 做单事件判定：把事件序号与当前最大序号比较，
// 给出有序/相等/乱序三态并计算迟到量。本包不依赖其他包。
package seq

// Kind 是单个事件相对当前 MaxSeen 的三态判定。
type Kind uint8

const (
	// InOrder 表示序号严格大于当前最大值（首个事件由调用方按有序处理）。
	InOrder Kind = iota + 1
	// Equal 表示序号等于当前最大值；相等不算乱序。
	Equal
	// Late 表示序号小于当前最大值，即乱序到达。
	Late
)

// String 返回三态的中文名称。
func (k Kind) String() string {
	switch k {
	case InOrder:
		return "有序"
	case Equal:
		return "相等"
	case Late:
		return "乱序"
	default:
		return "未知"
	}
}

// Valid 报告序号是否合法：Seq 必须 ≥ 1，Seq ≤ 0 非法。
func Valid(s int64) bool {
	return s >= 1
}

// Classify 在“已见过至少一个事件”的前提下，判定 s 相对 maxSeen 的三态。
// 首个事件（尚无 maxSeen）的处理由调用方负责，不应调用本函数。
func Classify(s, maxSeen int64) Kind {
	switch {
	case s > maxSeen:
		return InOrder
	case s == maxSeen:
		return Equal
	default:
		return Late
	}
}

// Lateness 返回乱序事件的迟到量 maxSeen-s；仅在 Classify 返回 Late 时有意义。
func Lateness(s, maxSeen int64) int64 {
	return maxSeen - s
}
