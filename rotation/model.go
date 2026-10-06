// Package rotation implements an ordered, peak-shaving rotation scheduler.
package rotation

import "fmt"

// Category is the controllability class of a user.
type Category int

const (
	// Exempt users are never limited and never notified.
	Exempt Category = iota
	// Reserved users are limited down to their reserved power.
	Reserved
	// Normal users are limited down to zero.
	Normal
)

// RejectKind classifies every rejected operation.
// Refusals are reported in the fixed precedence documented in DESIGN.md.
type RejectKind int

const (
	RejectInvalid       RejectKind = iota // 参数非法
	RejectClockBack                       // 时钟回退
	RejectNoOrder                         // 指令不存在或已取消
	RejectInSlot                          // 时段进行中
	RejectAfterDeadline                   // 已过截止
	RejectNoNotice                        // 通知不存在
)

// Error is the only error type returned by scheduler operations.
type Error struct {
	Kind RejectKind
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func errf(kind RejectKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// Group is a rotation group inside a region.
type Group struct {
	ID     int
	region *Region
	// accrued is the cumulative limited duration, always a multiple of slotLen.
	accrued int64
	// members lists user IDs in insertion order. Order does not affect
	// selection: selection cost depends only on the number of groups.
	members []string
}

// User belongs to exactly one group.
type User struct {
	ID       string
	category Category
	groupID  int
	// reservedPower is meaningful only for Reserved users.
	reservedPower int64
}

// Notice is one per-user, per-slot notification.
type Notice struct {
	UserID    string
	RegionID  string
	SlotStart int64
	// Limit is zero for Normal users and the reserved power for Reserved users.
	Limit int64
}

// Assessment records a user that had not confirmed when its slot began.
type Assessment struct {
	UserID    string
	RegionID  string
	SlotStart int64
}

// Region groups the static topology and per-region dynamic state.
type Region struct {
	ID      string
	slotLen int64
	groups  map[int]*Group
	// groupIDs is sorted ascending; ties in cumulative time break by it.
	groupIDs []int
	users    map[string]*User
}
