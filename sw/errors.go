package sw

import "errors"

var (
	ErrInvalidArgument      = errors.New("sw: invalid argument")
	ErrClockRollback        = errors.New("sw: clock rollback")
	ErrRegistrationNotFound = errors.New("sw: registration not found")
	ErrVersionNotFound      = errors.New("sw: version not found")
	ErrStateNotAllowed      = errors.New("sw: state not allowed")
	ErrTooFrequent          = errors.New("sw: too frequent")
)
