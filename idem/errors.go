package idem

import (
	"errors"
	"fmt"
)

// ErrFingerprintMismatch is returned when a cached or in-flight record for an
// idempotency key was created with a different request fingerprint.
var ErrFingerprintMismatch = errors.New("idem: fingerprint mismatch")

// retriableError marks an infrastructure-level failure whose result must not
// be cached.
type retriableError struct {
	err error
}

// Retriable wraps err to signal an infrastructure-level failure. A result
// ending in a retriable error is never cached and the key's slot is released
// immediately, allowing the next call to execute fn again.
func Retriable(err error) error {
	if err == nil {
		return nil
	}
	return &retriableError{err: err}
}

func (e *retriableError) Error() string {
	return fmt.Sprintf("retriable: %v", e.err)
}

func (e *retriableError) Unwrap() error {
	return e.err
}

// isRetriable reports whether err carries the retriable marker.
func isRetriable(err error) bool {
	var r *retriableError
	return errors.As(err, &r)
}

type panicErr struct {
	value any
}

func (e *panicErr) Error() string {
	return fmt.Sprintf("idem: fn panicked: %v", e.value)
}

// panicError converts a recovered panic value into a non-nil error.
func panicError(value any) error {
	if err, ok := value.(error); ok {
		return &panicErr{value: err}
	}
	return &panicErr{value: value}
}
