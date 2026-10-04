package spectate

import "errors"

var (
	ErrInvalidParam   = errors.New("spectate: invalid parameter")
	ErrClockRollback  = errors.New("spectate: clock rollback")
	ErrNotSpectating  = errors.New("spectate: viewer is not spectating")
	ErrAlreadyJoined  = errors.New("spectate: viewer already joined")
	ErrModeOff        = errors.New("spectate: mode is off")
	ErrNotFriend      = errors.New("spectate: viewer is not a friend")
	ErrSpectatorLimit = errors.New("spectate: non-judge spectator limit reached")
	ErrAlreadyEnded   = errors.New("spectate: match already ended")
	ErrInvalidMode    = errors.New("spectate: invalid mode")
)
