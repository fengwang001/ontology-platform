package approval

import "errors"

// 错误类别（可用 errors.Is 区分）。
var (
	// ErrInvalid 参数非法（空串、金额/now/T 越界等）。
	ErrInvalid = errors.New("approval: invalid argument")
	// ErrClock now 小于引擎当前时钟。
	ErrClock = errors.New("approval: clock moved backwards")
	// ErrNotFound 申请不存在（Submit 时表示申请已存在）。
	ErrNotFound = errors.New("approval: request not found")
	// ErrClosed 申请已终局。
	ErrClosed = errors.New("approval: request already closed")
	// ErrNotAssignee 操作者不是当前审批人。
	ErrNotAssignee = errors.New("approval: not the current assignee")
	// ErrRevoked 当前审批人额度已低于申请金额。
	ErrRevoked = errors.New("approval: approval authority revoked")
	// ErrNoApprover 提交时审批链上无合格候选。
	ErrNoApprover = errors.New("approval: no eligible approver")
)

// Outcome 是申请终局类型。
type Outcome int

const (
	// Pending 待决。
	Pending Outcome = iota
	// Approved 通过。
	Approved
	// Rejected 拒绝。
	Rejected
	// Expired 超时终局。
	Expired
)

// Status 描述申请在指定时刻做虚拟到期处理后的状态。
type Status struct {
	// Assignee 为当前审批人；终局时为空串。
	Assignee string
	// Ta 为当前审批人分配时刻；终局时为终局时刻。
	Ta int64
	// Outcome 终局类型；Pending 表示未终局。
	Outcome Outcome
	// FinalAt 终局时刻；未终局为 0。
	FinalAt int64
}
