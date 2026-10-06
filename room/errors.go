package room

// Reject 标识一次变更/查询被拒绝的原因。拒绝次序见规范：
//
//	参数非法 > 时钟回退 > 阶段不允许（含已终止）>
//	不在室或不在名单 > 权限不足 > 状态冲突。
type Reject string

const (
	RejectInvalidArg  Reject = "invalid_arg"  // 参数非法
	RejectClockRewind Reject = "clock_rewind" // 时钟回退
	RejectPhase       Reject = "phase"        // 阶段不允许（终态为 RejectTerminated）
	RejectNotPresent  Reject = "not_present"  // 玩家不在室 / 不在对局名单
	RejectNotOwner    Reject = "not_owner"    // 非房主，权限不足
	RejectConflict    Reject = "conflict"     // 状态冲突（已就绪/已满/已加入）
)

// RejectTerminated 是终态（已结束/已作废）之后所有变更类操作的拒绝原因。
const RejectTerminated Reject = "terminated"

// OpError 表示一次操作被裁决拒绝。它实现 error 接口。
type OpError struct{ Reason Reject }

func (e OpError) Error() string { return "room: rejected: " + string(e.Reason) }
