package routewatch

import "errors"

var (
	ErrInvalidParam  = errors.New("routewatch: invalid parameter")
	ErrClockRollback = errors.New("routewatch: clock rollback")
	ErrStopNotFound  = errors.New("routewatch: stop not found")
	ErrInvalidState  = errors.New("routewatch: stop state does not permit this operation")
	ErrStopSkipped   = errors.New("routewatch: stop was skipped")
	ErrOutOfOrder    = errors.New("routewatch: actual arrival must be strictly after previous stop departure")
)
