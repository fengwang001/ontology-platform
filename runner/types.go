package runner

import "errors"

// 运行器返回的哨兵错误。拒绝类别（用 errors.Is 区分）按判定优先级：
// 参数非法 → ErrClock → 不存在/已存在 → ErrState → ErrSelf → ErrNoAuthority。
var (
	// ErrArg 参数非法：空的 inst/def/p/a、掩码为零、T 越界等。
	ErrArg = errors.New("runner: illegal argument")
	// ErrClock now 越界（须在 0..1e15）或小于全局时钟。
	ErrClock = errors.New("runner: clock not monotonic or out of range")
	// ErrNotFound 定义或实例不存在。
	ErrNotFound = errors.New("runner: not found")
	// ErrExists Launch 时实例已存在。
	ErrExists = errors.New("runner: instance already exists")
	// ErrState 动作与实例状态不符（含到期处理后已 Failed 的实例）。
	ErrState = errors.New("runner: invalid instance state")
	// ErrSelf 审批人就是触发者本人。
	ErrSelf = errors.New("runner: self approval not allowed")
	// ErrNoAuthority 审批人此刻不持有位 63。
	ErrNoAuthority = errors.New("runner: approver lacks authority bit")
)

const (
	maxNow = int64(1_000_000_000_000_000) // now 上界 1e15
	maxT   = int64(1_000_000_000)         // T 上界 1e9
)

// Phase 是实例的生命周期状态。
type Phase int

const (
	// PendingPhase 已启动、等待下一步（可能因权限不足而 Suspended）。
	PendingPhase Phase = iota
	// RunningPhase 当前有一步正在执行。
	RunningPhase
	// CompletedPhase 全部步骤 Done。
	CompletedPhase
	// FailedPhase 终局失败（被拒绝或超时）。
	FailedPhase
)

// StepPhase 是单步状态。
type StepPhase int

const (
	// StepPending 尚未启动。
	StepPending StepPhase = iota
	// StepRunning 正在执行（Allow 放行或被 Override）。
	StepRunning
	// StepDone 已完成。
	StepDone
)

// Outcome 是终局类别。
type Outcome int

const (
	// None 尚未终局。
	None Outcome = iota
	// Completed 全部步骤完成。
	Completed
	// Rejected 被有审批权的第三人拒绝。
	Rejected
	// Expired 挂起超时。
	Expired
)

func (o Outcome) String() string {
	switch o {
	case Completed:
		return "Completed"
	case Rejected:
		return "Failed(Rejected)"
	case Expired:
		return "Failed(Expired)"
	default:
		return "None"
	}
}

// Kind 是审计条目类型。
type Kind int

const (
	// Allow 步骤以自身权限正常放行。
	Allow Kind = iota + 1
	// Deny 权限不足，实例进入 Suspended（成功结果，非错误）。
	Deny
	// Override 审批人越权放行当前一步。
	Override
	// RejectEv 审批人拒绝，实例终局。
	RejectEv
	// ExpireEv 挂起超时，实例终局。
	ExpireEv
)

func (k Kind) String() string {
	switch k {
	case Allow:
		return "Allow"
	case Deny:
		return "Deny"
	case Override:
		return "Override"
	case RejectEv:
		return "Reject"
	case ExpireEv:
		return "Expire"
	default:
		return "Unknown"
	}
}
