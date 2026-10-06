package traffic

import "errors"

// Distinguishable, exported error classes. They are returned in a fixed
// precedence order (see DESIGN.md) so callers can detect the exact
// reason an operation was rejected.
var (
	ErrInvalidArgument      = errors.New("invalid argument")
	ErrClockRewind          = errors.New("clock rewind")
	ErrLinkNotFound         = errors.New("link not found")
	ErrIncidentNotFound     = errors.New("incident not found")
	ErrIncidentAlreadyEnded = errors.New("incident already ended")
	ErrRatioOutOfRange      = errors.New("reduction ratio out of range")
	ErrArrivalExceedsCap    = errors.New("arrival flow exceeds capacity")
)
