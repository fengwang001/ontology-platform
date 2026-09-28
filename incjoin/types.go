// Package incjoin 增量维护两张多重集（multiset）表的等值内连接。
//
// 左右两张表均为 (Key, Value) -> multiplicity 的多重集；Apply 按批次接收
// 两侧的带符号变更，输出批前/批后内连接结果之差（差分），并原子地更新
// 两张表与物化的连接结果。详见同包 README 与 joiner.go。
package incjoin

import "fmt"

// Row 表示一批输入变更中的一行：对 (Key, Value) 的重数施加带符号增量 Mult。
// Mult > 0 为插入，Mult < 0 为删除，Mult == 0 非法。
type Row struct {
	Key   string
	Value string
	Mult  int64
}

// String 用于日志输出。
func (r Row) String() string {
	return fmt.Sprintf("(key=%q,val=%q,mult=%d)", r.Key, r.Value, r.Mult)
}

// Change 是一次 Apply 调用的输入批：左右两侧各自的变更行。
// 两侧可以同时非空（同时改两张表）。
type Change struct {
	Left  []Row
	Right []Row
}

// DiffEntry 是输出差分（以及全量视图）中的一条结果元组。
// 重数 Mult 对连接多重集计数：等于两侧匹配行重数之积。
// 在差分中 Mult 为带符号增量（正=新增，负=移除），永不为 0；
// 在全量快照中 Mult 为当前非负重数。
type DiffEntry struct {
	Key      string
	LeftVal  string
	RightVal string
	Mult     int64
}

// String 用于日志输出。
func (e DiffEntry) String() string {
	return fmt.Sprintf("(key=%q,l=%q,r=%q,mult=%d)", e.Key, e.LeftVal, e.RightVal, e.Mult)
}

// RejectReason 是批被拒绝的可区分原因。
type RejectReason string

const (
	// ReasonEmptyKey 空值：连接键为空（不允许 null/空串）。
	ReasonEmptyKey RejectReason = "EMPTY_KEY"
	// ReasonEmptyValue 空值：任一侧的值为空。
	ReasonEmptyValue RejectReason = "EMPTY_VALUE"
	// ReasonInvalidMultSign 变更符号非法：变更重数为 0（必须为正或负）。
	ReasonInvalidMultSign RejectReason = "INVALID_MULT_SIGN"
	// ReasonDeleteNonexistent 删除不存在的行：批后某 (键,值) 的重数为负。
	ReasonDeleteNonexistent RejectReason = "DELETE_NONEXISTENT_ROW"
	// ReasonResultLimitExceeded 结果元组数超限：批后连接结果总元组数超过上限。
	ReasonResultLimitExceeded RejectReason = "RESULT_TUPLE_LIMIT_EXCEEDED"
	// ReasonMultOverflow 重数/乘积超出 int64 可表示范围（防御性拒绝）。
	ReasonMultOverflow RejectReason = "MULTIPLICITY_OVERFLOW"
)

// RejectError 描述一批被拒绝的原因与定位信息。被拒绝的批不会改变任何状态。
type RejectError struct {
	Reason RejectReason
	// Side 为 "left" / "right"，仅与侧别相关的原因填充；行级非法在对应侧定位。
	Side string
	Key  string
	Val  string
	Msg  string
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("incjoin: batch rejected: %s: %s", e.Reason, e.Msg)
}
