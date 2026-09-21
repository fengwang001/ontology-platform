package ttlcache

import "errors"

// ErrInvalidCapacity is returned by New when capacity is not positive.
// Callers can check it with errors.Is.
var ErrInvalidCapacity = errors.New("ttlcache: capacity must be greater than zero")

// ErrInvalidTTL is returned by Put when ttlMillis is not positive.
// A non-positive TTL is never treated as "never expires".
var ErrInvalidTTL = errors.New("ttlcache: ttlMillis must be greater than zero")
