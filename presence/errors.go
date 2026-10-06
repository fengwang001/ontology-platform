package presence

import "errors"

// Distinguishable sentinel errors. Rejection precedence is:
// ErrInvalidArgument > ErrClockBackwards > ErrNotFound > ErrState.
var (
	ErrInvalidArgument     = errors.New("presence: invalid argument")
	ErrClockBackwards      = errors.New("presence: clock moved backwards")
	ErrNotFound            = errors.New("presence: user or device not found")
	ErrTooManyDevices      = errors.New("presence: too many devices")
	ErrAlreadySubscribed   = errors.New("presence: cannot subscribe twice")
	ErrCannotSubscribeSelf = errors.New("presence: cannot subscribe to self")
	ErrCannotBlockSelf     = errors.New("presence: cannot block self")
)

// IsStateError reports whether err is a "state not allowed" rejection.
func IsStateError(err error) bool {
	return errors.Is(err, ErrAlreadySubscribed) ||
		errors.Is(err, ErrCannotSubscribeSelf) ||
		errors.Is(err, ErrCannotBlockSelf)
}
