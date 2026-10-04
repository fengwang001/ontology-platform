// Package voice 提供禁言、解禁，禁言与补麦编排由房间统一保证。
package voice

import "ontology/internal/core"

type Room = core.Room

func New(m int) *Room { return core.New(m) }

// Mute 禁言 target 至 until（until>now）。target 在麦则立即下麦，排队中保留原位。
func Mute(room *Room, now, by, target, until int64) error {
	return room.Mute(now, by, target, until)
}

// Unmute 解除 target 的生效禁言。
func Unmute(room *Room, now, by, target int64) error {
	return room.Unmute(now, by, target)
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
