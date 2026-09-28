package groupagg

import "fmt"

// RejectReason 是批次被拒绝的可区分原因。
type RejectReason int

const (
	// ReasonDuplicateInsert 重复插入：Insert 的行 ID 在批次开始时（或批次内此前的 Insert 后）已存在。
	ReasonDuplicateInsert RejectReason = iota + 1
	// ReasonUpdateMissing 更新不存在的行。
	ReasonUpdateMissing
	// ReasonDeleteMissing 删除不存在的行。
	ReasonDeleteMissing
	// ReasonEmptyGroupKey 空分组键。
	ReasonEmptyGroupKey
	// ReasonEmptyID 空行 ID。
	ReasonEmptyID
	// ReasonUnknownOp 未知操作类型。
	ReasonUnknownOp
	// ReasonTooManyGroups 组数超限：操作会使非空分组数超过上限。
	ReasonTooManyGroups
)

// String 返回原因的稳定英文标识，便于日志与断言区分。
func (r RejectReason) String() string {
	switch r {
	case ReasonDuplicateInsert:
		return "DUPLICATE_INSERT"
	case ReasonUpdateMissing:
		return "UPDATE_MISSING"
	case ReasonDeleteMissing:
		return "DELETE_MISSING"
	case ReasonEmptyGroupKey:
		return "EMPTY_GROUP_KEY"
	case ReasonEmptyID:
		return "EMPTY_ID"
	case ReasonUnknownOp:
		return "UNKNOWN_OP"
	case ReasonTooManyGroups:
		return "TOO_MANY_GROUPS"
	default:
		return "UNKNOWN"
	}
}

// RejectError 描述一个批次为何被拒绝以及触发拒绝的操作下标。
type RejectError struct {
	Reason  RejectReason
	OpIndex int // 批次中触发拒绝的操作下标（从 0 开始）
	Message string
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("batch rejected at op[%d] (%s): %s", e.OpIndex, e.Reason, e.Message)
}
