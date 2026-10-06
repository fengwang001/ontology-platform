package pharmacy

import "errors"

var (
	ErrInvalidArgument     = errors.New("invalid argument")
	ErrClockRollback       = errors.New("clock rollback")
	ErrNotFound            = errors.New("object not found")
	ErrInvalidState        = errors.New("invalid state")
	ErrPrescriptionExpired = errors.New("prescription expired")
	ErrOutOfStock          = errors.New("out of stock")
	ErrNoActiveReservation = errors.New("no active reservation")
)
