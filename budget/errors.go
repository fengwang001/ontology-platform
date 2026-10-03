package budget

// Reason 是操作被拒绝的原因。
type Reason string

const (
	ReasonInvalid    Reason = "invalid_parameter" // 参数非法
	ReasonNotFound   Reason = "not_found"         // 组件或任务不存在
	ReasonDuplicate  Reason = "duplicate"         // 名称或任务编号重复
	ReasonCapacity   Reason = "capacity_full"     // 组件数或任务数已满
	ReasonTooLarge   Reason = "too_large"         // Dmax+H 超过 1_000_000
	ReasonInfeasible Reason = "infeasible"        // Θ=Π 时仍不可调度
	ReasonOverloaded Reason = "overloaded"        // 全局带宽之和大于 1
)

// RejectError 携带拒绝原因与可复现的判定依据。
type RejectError struct {
	Reason   Reason
	Op       string // 发生拒绝的操作
	Detail   string // 人类可读的判定依据
	ViolateT int64  // 不可行时 Θ=Π 下最小的违反点；仅因利用率>1 时为 0
}

func (e *RejectError) Error() string {
	return string(e.Reason)
}

func rejectError(op string, reason Reason, detail string) *RejectError {
	return &RejectError{Reason: reason, Op: op, Detail: detail}
}
