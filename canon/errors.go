package canon

import "errors"

// Distinct, mutually decidable limit / parse errors.
var (
	// ErrTooLong rejects URLs longer than Limits.MaxURLLen.
	ErrTooLong = errors.New("canon: URL exceeds maximum length")
	// ErrTooManySegments rejects paths with more than Limits.MaxSegments.
	ErrTooManySegments = errors.New("canon: path exceeds maximum segment count")
	// ErrTooManyQueryItems rejects queries with more than MaxQueryItems.
	ErrTooManyQueryItems = errors.New("canon: query exceeds maximum item count")
	// ErrMalformed rejects URLs that cannot be parsed.
	ErrMalformed = errors.New("canon: malformed URL")
)

// LimitError reports which resource limit was exceeded.
type LimitError struct{ err error }

func (e *LimitError) Error() string { return e.err.Error() }
func (e *LimitError) Unwrap() error { return e.err }

func limitErr(target error) error { return &LimitError{err: target} }

// IsLimit reports whether err is any resource-limit error.
func IsLimit(err error) bool {
	var le *LimitError
	return errors.As(err, &le)
}

// OffsetError carries a malformed-escape error with its absolute byte offset.
type OffsetError struct {
	Offset int
	Kind   string
	err    error
}

func (e *OffsetError) Error() string { return e.err.Error() }
func (e *OffsetError) Unwrap() error { return e.err }
