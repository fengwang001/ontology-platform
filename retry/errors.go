package retry

import "errors"

// ErrExhausted is returned (wrapped) when all attempts fail.
var ErrExhausted = errors.New("retry: attempts exhausted")

// ErrAborted is returned (wrapped) when a permanent error stops the run.
var ErrAborted = errors.New("retry: aborted by permanent error")

// permanent marks an error as not retryable.
type permanent struct{ err error }

func (e *permanent) Error() string { return e.err.Error() }
func (e *permanent) Unwrap() error { return e.err }

// Permanent wraps err so that Runner.Do stops immediately without
// waiting or retrying. A nil err stays nil.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanent{err: err}
}

// asPermanent unwraps err one level if it was marked permanent.
func asPermanent(err error) (error, bool) {
	var p *permanent
	if errors.As(err, &p) {
		return p.err, true
	}
	return nil, false
}
