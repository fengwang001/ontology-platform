package chunked

import (
	"errors"
	"fmt"
)

var (
	// ErrClosed is returned by Write after the message completed.
	ErrClosed = errors.New("chunked: decoder already finished")
	// ErrIncomplete is returned by Close when the stream ended before
	// the message completed; the Error's State tells where it stopped.
	ErrIncomplete = errors.New("chunked: stream ended before message completed")
	// ErrHalfCRLF is returned by Close when the stream ended between
	// a CR and its LF. It also matches ErrIncomplete via errors.Is.
	ErrHalfCRLF = fmt.Errorf("%w: carriage return without line feed", ErrIncomplete)
	// ErrTooManyTrailers indicates the trailer line count limit was hit.
	ErrTooManyTrailers = errors.New("chunked: too many trailer lines")
	// ErrChunkTooLarge indicates a chunk exceeded the per-chunk limit.
	ErrChunkTooLarge = errors.New("chunked: chunk size exceeds limit")
	// ErrBodyTooLarge indicates the decoded body exceeded the total limit.
	ErrBodyTooLarge = errors.New("chunked: total body size exceeds limit")
)

// Error describes a decoding failure. Offset is the 0-based byte
// offset in the input stream at which the problem was detected (for
// Close errors, the offset where input stopped). State is the
// decoder state at that moment. Err is one of the sentinel errors in
// this package or in hexline/frame; use errors.Is to classify.
type Error struct {
	Offset int
	State  State
	Err    error
}

// Error returns a human-readable description including the offset.
func (e *Error) Error() string {
	return fmt.Sprintf("chunked: offset %d (%s): %v", e.Offset, e.State, e.Err)
}

// Unwrap returns the underlying sentinel error.
func (e *Error) Unwrap() error {
	return e.Err
}
