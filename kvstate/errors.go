package kvstate

import "errors"

var (
	// ErrEmptyKey indicates that a write used an empty key.
	ErrEmptyKey = errors.New("kvstate: key must not be empty")
	// ErrInvalidTTL indicates that a write used a non-positive expiration duration.
	ErrInvalidTTL = errors.New("kvstate: ttl must be greater than zero")
)
