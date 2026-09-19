package idem

import (
	"errors"
	"fmt"
)

// ErrFingerprintMismatch is returned when a key is reused with a request
// fingerprint different from the one stored for that key. The stored record
// is never modified in this case. Match it with errors.Is.
var ErrFingerprintMismatch = errors.New("idem: fingerprint mismatch")

// retriableError marks an infrastructure-class failure whose outcome must
// not be cached: the key's slot is released immediately.
type retriableError struct {
	err error
}

func (e retriableError) Error() string { return e.err.Error() }
func (e retriableError) Unwrap() error { return e.err }

// Retriable wraps err to signal an infrastructure-class failure. When fn
// passed to Executor.Do returns a wrapped error, the outcome is not cached
// and the next call with the same key re-executes fn. A nil err yields nil.
func Retriable(err error) error {
	if err == nil {
		return nil
	}
	return retriableError{err: err}
}

// isRetriable reports whether err carries the retriable marker.
func isRetriable(err error) bool {
	var r retriableError
	return errors.As(err, &r)
}

// panicError converts a recovered panic value into an error so a panicking
// fn cannot crash the caller's process or poison the key.
type panicError struct {
	value any
}

func (e panicError) Error() string {
	return fmt.Sprintf("idem: fn panicked: %v", e.value)
}
