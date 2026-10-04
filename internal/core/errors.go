package core

import "errors"

var (
	ErrBadArgument  = errors.New("bad argument")
	ErrClockRewind  = errors.New("clock rewind")
	ErrNotInRoom    = errors.New("actor not in room")
	ErrNoTarget     = errors.New("target not in room")
	ErrLowLevel     = errors.New("insufficient level")
	ErrSuppressed   = errors.New("muted by higher level")
	ErrMustTransfer = errors.New("owner must transfer before leave")
	ErrNotMuted     = errors.New("target is not muted")
	ErrMuted        = errors.New("target is muted")
	ErrDuplicate    = errors.New("duplicate operation")
	ErrNotInMic     = errors.New("user not on mic or queue")
)
