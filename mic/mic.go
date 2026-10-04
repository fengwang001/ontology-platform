// Package mic 管理麦位占用与排麦队列。
package mic

import "ontology/internal/core"

type Room = core.Room

func New(m int) *Room { return core.New(m) }

// TakeMic：无空麦则排队尾；禁言中拒绝；已在麦/队列报重复。
func TakeMic(room *Room, now, u int64) error { return room.TakeMic(now, u) }

// DropMic：在麦下麦，在队列出队。
func DropMic(room *Room, now, u int64) error { return room.DropMic(now, u) }

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
