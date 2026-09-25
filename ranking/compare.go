package ranking

import "strings"

// compareValue 比较两个排序值。Go 中 +0.0 == -0.0 成立，
// 因此二者自然被视为相等；NaN 行在进入比较前已被拒绝。
func compareValue(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// defaultCompare 是默认行比较器：先按排序值升序，并列时按行 ID 升序。
// 该比较是严格的弱序，不依赖输入下标，保证输出可复现。
func defaultCompare(a, b Row) int {
	if c := compareValue(a.Value, b.Value); c != 0 {
		return c
	}
	return strings.Compare(a.ID, b.ID)
}

// descendingCompare 仅反转排序值方向；并列时仍按行 ID 升序，
// 因此降序结果不是升序结果的简单倒置。
func descendingCompare(a, b Row) int {
	if c := compareValue(b.Value, a.Value); c != 0 {
		return c
	}
	return strings.Compare(a.ID, b.ID)
}

// resolveCompare 按选项选出实际使用的比较器。
func resolveCompare(opts Options) func(a, b Row) int {
	if opts.Compare != nil {
		return opts.Compare
	}
	if opts.Descending {
		return descendingCompare
	}
	return defaultCompare
}
