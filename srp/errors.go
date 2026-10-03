package srp

import "fmt"

// Reason 描述一次被拒绝操作的原因码。
type Reason string

const (
	ReasonInvalidArgument    Reason = "invalid_argument"     // 参数非法
	ReasonNotFound           Reason = "not_found"            // 任务、作业或资源不存在
	ReasonDuplicate          Reason = "duplicate"            // 编号重复
	ReasonCapacityExceeded   Reason = "capacity_exceeded"    // 容量已满
	ReasonNotTopOfStack      Reason = "not_top_of_stack"     // 非栈顶
	ReasonPreemptionLevelLow Reason = "preemption_level_low" // 抢占层级不足
	ReasonCeilingBlocked     Reason = "ceiling_blocked"      // 系统天花板拦截
	ReasonOverClaim          Reason = "over_claim"           // 超出任务声明 μ
	ReasonUnitUnavailable    Reason = "unit_unavailable"     // 单元不足
	ReasonNotHeld            Reason = "not_held"             // 归还超过已持有量
	ReasonStillHolding       Reason = "still_holding"        // Finish 时仍持有资源
	ReasonTaskInUse          Reason = "task_in_use"          // 任务有未结束作业
)

// Error 是所有拒绝返回的错误，携带稳定的原因码与人类可读说明。
type Error struct {
	Reason  Reason
	Message string
}

func (e *Error) Error() string { return string(e.Reason) + ": " + e.Message }

func errf(r Reason, format string, args ...any) error {
	return &Error{Reason: r, Message: fmt.Sprintf(format, args...)}
}

func errInvalid(format string, args ...any) error {
	return errf(ReasonInvalidArgument, format, args...)
}

func errNotFound(format string, args ...any) error {
	return errf(ReasonNotFound, format, args...)
}

func errDuplicate(format string, args ...any) error {
	return errf(ReasonDuplicate, format, args...)
}

func errCapacity(format string, args ...any) error {
	return errf(ReasonCapacityExceeded, format, args...)
}
