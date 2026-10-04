package budget

import "fmt"

// RejectReason 区分操作被拒绝的原因，判定按下列顺序只报第一个。
type RejectReason int

const (
	// RejectInvalidParam 参数非法（空名称、Π 越界、任务参数越界等）。
	RejectInvalidParam RejectReason = iota
	// RejectNotFound 组件或任务不存在（RemoveTask/Compact/Budget 用）。
	RejectNotFound
	// RejectDuplicate 组件名称或组件内任务编号重复。
	RejectDuplicate
	// RejectCapacity 容量已满（组件数或单组件任务数超过上限）。
	RejectCapacity
	// RejectTooLarge 规模过大（Dmax+H 超过 10^6，H 计算防溢出提前停止）。
	RejectTooLarge
	// RejectInfeasible 任务集在 Θ=Π 下仍不可行。
	RejectInfeasible
	// RejectOverload 全局带宽 Σθ/Π 超过 1。
	RejectOverload
)

func (r RejectReason) String() string {
	switch r {
	case RejectInvalidParam:
		return "invalid parameter"
	case RejectNotFound:
		return "not found"
	case RejectDuplicate:
		return "duplicate"
	case RejectCapacity:
		return "capacity full"
	case RejectTooLarge:
		return "too large"
	case RejectInfeasible:
		return "infeasible"
	case RejectOverload:
		return "overload"
	}
	return "unknown"
}

// RejectError 描述一次被拒绝的操作，携带原因与相关数值。
type RejectError struct {
	Reason RejectReason
	Detail string
	// Theta 为拒绝时（尝试）使用的预算，不可行时为 Π。
	Theta int64
	// ViolationT 为 Θ=Π 时最小的违反点 t；仅因 ΣC/T>1 不可行时为 0。
	ViolationT int64
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("budget: %s (%s)", e.Reason, e.Detail)
}
