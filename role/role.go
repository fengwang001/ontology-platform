// Package role 提供角色等级、任免与房主移交能力。
package role

import "ontology/internal/core"

const (
	Owner  = core.Owner
	Admin  = core.Admin
	Member = core.Member
)

type Room = core.Room

// New 创建麦位数为 m 的房间，麦位编号 0..m-1。m 必须在 1..64。
func New(m int) *Room { return core.New(m) }

func Join(room *Room, now, u int64) error { return room.Join(now, u) }

func Leave(room *Room, now, u int64) error { return room.Leave(now, u) }

// SetRole 仅可任免为 Admin 或 Member；by 等级须严格高于 target 当前等级与目标等级。
func SetRole(room *Room, now, by, target int64, r int) error {
	return room.SetRole(now, by, target, r)
}

// Transfer 转让 Owner：by 须为 Owner，转让后 by 降为 Admin。
func Transfer(room *Room, now, by, target int64) error {
	return room.Transfer(now, by, target)
}

var (
	ErrBadArgument  = core.ErrBadArgument
	ErrClockRewind  = core.ErrClockRewind
	ErrNotInRoom    = core.ErrNotInRoom
	ErrNoTarget     = core.ErrNoTarget
	ErrLowLevel     = core.ErrLowLevel
	ErrSuppressed   = core.ErrSuppressed
	ErrMustTransfer = core.ErrMustTransfer
	ErrNotMuted     = core.ErrNotMuted
	ErrMuted        = core.ErrMuted
	ErrDuplicate    = core.ErrDuplicate
	ErrNotInMic     = core.ErrNotInMic
)
