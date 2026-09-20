package retry

import "errors"

// ErrExhausted is returned when all attempts fail.
var ErrExhausted = errors.New("retry: attempts exhausted")

// ErrAborted is returned when a permanent (non-retryable) error stops the run.
var ErrAborted = errors.New("retry: aborted by permanent error")

// permanentError marks an error as non-retryable.
type permanentError struct {
	err error
}

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// Permanent wraps err so that Runner.Do stops immediately instead of
// retrying. A nil err returns nil.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{err: err}
}

// asPermanent reports whether err (or anything it wraps) is permanent,
// and if so returns the innermost wrapped cause.
func asPermanent(err error) (error, bool) {
	var pe *permanentError
	if errors.As(err, &pe) {
		return pe.err, true
	}
	return nil, false
}
