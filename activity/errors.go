package activity

import "errors"

var (
	ErrConfig   = errors.New("activity: invalid config")
	ErrArgument = errors.New("activity: invalid argument")
	ErrClock    = errors.New("activity: clock moved backwards")
	ErrNotFound = errors.New("activity: not found")
	ErrExists   = errors.New("activity: already exists")
	ErrStale    = errors.New("activity: stale attempt")
	ErrTerminal = errors.New("activity: already terminal")
	ErrState    = errors.New("activity: invalid state")
)
