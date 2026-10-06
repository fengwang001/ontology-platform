package planningpoker

import "errors"

// 被拒绝的操作按固定次序只报第一个错误。
var (
	ErrInvalidArgument = errors.New("planningpoker: invalid argument")
	ErrClockRewind     = errors.New("planningpoker: clock moved backwards")
	ErrNotInSession    = errors.New("planningpoker: caller is not in the session")
	ErrForbidden       = errors.New("planningpoker: permission denied")
	ErrState           = errors.New("planningpoker: operation not allowed in current state")
	ErrNoVotes         = errors.New("planningpoker: nobody has voted")
)
