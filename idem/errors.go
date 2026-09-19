package idem

import "errors"

// ErrFingerprintMismatch is returned when a key is reused with a
// different request fingerprint. The existing record is never
// overwritten and fn is never executed in that case.
var ErrFingerprintMismatch = errors.New("idem: fingerprint mismatch")

// retriableError marks an infrastructure-class failure whose outcome
// must not be cached; the key slot is released immediately.
type retriableError struct{ err error }

func (e retriableError) Error() string { return "idem: retriable: " + e.err.Error() }
func (e retriableError) Unwrap() error { return e.err }

// Retriable wraps err to mark it as an infrastructure-class failure.
// When fn returns a retriable error, the outcome is not cached and the
// next call with the same key re-executes fn.
func Retriable(err error) error {
	if err == nil {
		return nil
	}
	return retriableError{err: err}
}

func isRetriable(err error) bool {
	var r retriableError
	return errors.As(err, &r)
}
