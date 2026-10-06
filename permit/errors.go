package permit

import "fmt"

// RuleCode 为互斥、有严格优先次序的拒绝原因。
type RuleCode int

const (
	// ErrInvalidParam 参数非法（空 ID、车道数 <=0、时段非法、优先级非法、ID 重复等）。
	ErrInvalidParam RuleCode = iota
	// ErrClockRollback 操作时刻早于上一次被接受操作的时刻。
	ErrClockRollback
	// ErrSegmentNotFound 路段不存在（含绕行路线引用了不存在路段，于路网构建期）。
	ErrSegmentNotFound
	// ErrPermitNotFound 许可不存在。
	ErrPermitNotFound
	// ErrPermitEnded 许可已结束（延期/撤销时）。
	ErrPermitEnded
	// ErrLanesExceed 封闭车道数超过路段车道数。
	ErrLanesExceed
	// ErrSameSegment 同路段冲突（三类冲突同时成立时只报本类）。
	ErrSameSegment
	// ErrDetour 绕行冲突。
	ErrDetour
	// ErrCorridorCap 走廊并发封闭数超上限。
	ErrCorridorCap
	// ErrStartBeforeNow 申请起始时刻早于操作时刻。
	ErrStartBeforeNow
)

// RuleError 携带可区分的拒绝原因及证据（与谁冲突），便于精确复现与日志。
type RuleError struct {
	Code    RuleCode
	Message string
	// Witness 为判定证据：冲突许可 ID 或走廊 ID。
	Witness []string
}

func (e *RuleError) Error() string {
	if len(e.Witness) > 0 {
		return fmt.Sprintf("%s: %s (witness=%v)", e.Code, e.Message, e.Witness)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (c RuleCode) String() string {
	switch c {
	case ErrInvalidParam:
		return "INVALID_PARAM"
	case ErrClockRollback:
		return "CLOCK_ROLLBACK"
	case ErrSegmentNotFound:
		return "SEGMENT_NOT_FOUND"
	case ErrPermitNotFound:
		return "PERMIT_NOT_FOUND"
	case ErrPermitEnded:
		return "PERMIT_ENDED"
	case ErrLanesExceed:
		return "LANES_EXCEED"
	case ErrSameSegment:
		return "SAME_SEGMENT_CONFLICT"
	case ErrDetour:
		return "DETOUR_CONFLICT"
	case ErrCorridorCap:
		return "CORRIDOR_CAP_EXCEEDED"
	case ErrStartBeforeNow:
		return "START_BEFORE_NOW"
	default:
		return fmt.Sprintf("CODE(%d)", int(c))
	}
}

func ruleErr(code RuleCode, msg string, witness ...string) *RuleError {
	return &RuleError{Code: code, Message: msg, Witness: witness}
}
