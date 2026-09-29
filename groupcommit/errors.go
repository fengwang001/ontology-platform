package groupcommit

import "errors"

// Immediate, distinguishable rejection reasons. None of these consume a
// sequence number or place a request on the queue.
var (
	// ErrEmptyPayload is returned when a submit carries no bytes.
	ErrEmptyPayload = errors.New("groupcommit: empty payload")
	// ErrPayloadTooLarge is returned when one payload alone exceeds MaxBytes.
	ErrPayloadTooLarge = errors.New("groupcommit: payload exceeds max batch bytes")
	// ErrClosed is returned when Submit is called after Close.
	ErrClosed = errors.New("groupcommit: committer is closed")

	// ErrInvalidMaxEntries is returned when MaxEntries is not positive.
	ErrInvalidMaxEntries = errors.New("groupcommit: MaxEntries must be positive")
	// ErrInvalidMaxBytes is returned when MaxBytes is not positive.
	ErrInvalidMaxBytes = errors.New("groupcommit: MaxBytes must be positive")
	// ErrNilPersister is returned when no Persister is supplied.
	ErrNilPersister = errors.New("groupcommit: Persister must not be nil")

	// ErrBatchFailed wraps the persistence failure shared by every entry
	// in a failed batch.
	ErrBatchFailed = errors.New("groupcommit: batch persistence failed")
)
