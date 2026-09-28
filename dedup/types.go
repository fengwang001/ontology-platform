// Package dedup 在撤回式变更流上按分组增量维护去重计数。
//
// 每个 (组, 值) 维护插入与撤回的净次数（多重性）；组的去重计数等于
// 该组内多重性为正的值的个数。变更以批为单位提交：一批要么整体生效，
// 要么因任意一条非法而整体拒绝（不改变任何内部状态，也不产生日志）。
package dedup

import "strconv"

// RejectReason 是批被拒绝的可区分原因。
type RejectReason string

const (
	// ReasonEmptyGroup：条目的组名为空字符串。
	ReasonEmptyGroup RejectReason = "empty group name"
	// ReasonEmptyValue：条目的值为空字符串。
	ReasonEmptyValue RejectReason = "empty value"
	// ReasonInvalidSign：变更符号非法（只允许 +1 插入或 -1 撤回）。
	ReasonInvalidSign RejectReason = "invalid change sign"
	// ReasonWithdrawZero：撤回一个当前净次数为零的值（含批内此前各条
	// 已生效后的状态），会使多重性变为负。
	ReasonWithdrawZero RejectReason = "withdraw exceeds current multiplicity"
	// ReasonTooManyEntries：批内条目数超过构造时设定的上限。
	ReasonTooManyEntries RejectReason = "too many entries in batch"
)

// BatchError 在批被拒绝时返回，携带机器可判别的原因与定位信息。
type BatchError struct {
	// Reason 是拒绝原因。
	Reason RejectReason
	// Index 是触发拒绝的条目在批内的下标；批级错误（如条目超限）为 -1。
	Index int
	// Group、Value 是触发条目的组与值（可能为空，这本身即是非法原因）。
	Group string
	Value string
}

func (e *BatchError) Error() string {
	if e.Index >= 0 {
		return "dedup: batch rejected at entry " + strconv.Itoa(e.Index) +
			": " + string(e.Reason)
	}
	return "dedup: batch rejected: " + string(e.Reason)
}

// Entry 是变更流中的一条：对 (Group, Value) 做一次 Delta 变更。
// Delta 只允许 +1（插入）或 -1（撤回）。
type Entry struct {
	Group string
	Value string
	Delta int
}

// GroupChange 是日志中的一条输出：某个组在批前与批后的去重计数。
// 下游按日志顺序对各组累加 After-Before 即可重放出正确的去重计数。
type GroupChange struct {
	Group string
	// Before 是该批应用前组内多重性为正的值的个数。
	Before int
	// After 是该批全部应用后组内多重性为正的值的个数。
	After int
}

// Delta 返回该组去重计数的净变化（After - Before，可能为 0）。
func (c GroupChange) Delta() int { return c.After - c.Before }
