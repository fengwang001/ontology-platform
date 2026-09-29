package delegation

import "time"

// Subject 标识一个权限主体（用户、服务账号等）。
type Subject string

// Permission 标识一项可被委托的权限。
type Permission string

// edgeStatus 描述一条委托在当前图状态下的有效性。
type edgeStatus int

const (
	statusActive edgeStatus = iota
	statusRevoked
	statusInvalidRedelegate
	statusMissingAuthority
)

// Edge 表示一条权限委托：Delegator 将 Permission 委托给 Delegatee。
type Edge struct {
	ID            string
	Delegator     Subject
	Delegatee     Subject
	Permission    Permission
	ExpiresAt     time.Time
	CanRedelegate bool
	ParentID      string

	status     edgeStatus
	invalidity RejectReason
}

// RejectReason 是被拒绝操作的可区分原因。
type RejectReason int

const (
	ReasonUnknown RejectReason = iota
	ReasonNoAuthority
	ReasonRedelegateForbidden
	ReasonCycle
	ReasonExpired
	ReasonDuplicate
	ReasonSelfDelegation
)
