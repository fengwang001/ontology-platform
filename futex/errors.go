package futex

import "errors"

var (
	ErrInvalid   = errors.New("futex: invalid argument")
	ErrBusy      = errors.New("futex: thread already waiting")
	ErrChanged   = errors.New("futex: memory word changed")
	ErrTimeout   = errors.New("futex: deadline already expired")
	ErrFull      = errors.New("futex: wait queue full")
	ErrNotWait   = errors.New("futex: thread is not waiting")
	ErrClockBack = errors.New("futex: clock cannot move backwards")
)
