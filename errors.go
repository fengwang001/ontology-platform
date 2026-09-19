package ontology

import (
	"errors"
	"time"
)

// ErrInvalidRequest is returned when a caller requests a negative number of
// tokens. It is a parameter error and is therefore distinct from the
// "not enough tokens" condition reported by ErrInsufficientTokens.
var ErrInvalidRequest = errors.New("ontology: requested token count must not be negative")

// ErrRequestExceedsCapacity is returned when a request asks for more tokens
// than a bucket can ever hold. Such a request can never be satisfied, no
// matter how long the caller waits, so rejecting it immediately avoids a
// permanent wait.
var ErrRequestExceedsCapacity = errors.New("ontology: requested token count exceeds bucket capacity")

// ErrTimeReversed is returned when Allow receives a timestamp earlier than
// the one seen on the previous call for the same tenant.
//
// Deterministic policy: the call is rejected. No (negative) tokens are
// refilled, no tokens are consumed, the bucket is never cleared, and the
// stored last-seen timestamp is left untouched. A later call with a
// timestamp at or after the stored one behaves exactly as if the reversed
// call had never happened.
var ErrTimeReversed = errors.New("ontology: timestamp is earlier than the previous call")

// ErrInsufficientTokens is the sentinel for the "valid request, but not
// enough tokens right now" category.
var ErrInsufficientTokens = errors.New("ontology: insufficient tokens")

// InsufficientTokensError is returned by Allow when a request is valid but
// the bucket does not currently contain enough tokens.
//
// RetryAfter is the exact time that must elapse (measured from the call's
// now) before at least Requested tokens are available, assuming no other
// consumption happens. It is rounded up to whole nanoseconds, so it never
// under-estimates the wait by more than one nanosecond.
type InsufficientTokensError struct {
	// Requested is the number of tokens the call asked for.
	Requested int64
	// Available is the whole number of tokens available at the call's now.
	Available int64
	// RetryAfter is the minimum wait until Requested tokens are available.
	RetryAfter time.Duration
}

func (e *InsufficientTokensError) Error() string {
	return "ontology: insufficient tokens"
}

// Is makes errors.Is(err, ErrInsufficientTokens) work, so callers can test
// the error category without a type assertion while still being able to
// inspect the concrete *InsufficientTokensError for RetryAfter.
func (e *InsufficientTokensError) Is(target error) bool {
	return target == ErrInsufficientTokens
}
