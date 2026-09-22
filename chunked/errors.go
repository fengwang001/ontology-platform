package chunked

import (
	"errors"
	"fmt"

	"ontology/frame"
	"ontology/hexline"
)

// Error sentinels. Every failure reported by the decoder wraps exactly
// one of these, so errors.Is distinguishes them. The first four are
// defined by the lower-level packages and re-exported here.
var (
	ErrBadSize         = hexline.ErrBadSize   // non-hexadecimal chunk size
	ErrUnclosedQuote   = hexline.ErrUnclosedQuote // unterminated quoted-string
	ErrSizeLineTooLong = frame.ErrLineTooLong // size line exceeds MaxSizeLine
	ErrMissingCRLF     = frame.ErrBadCRLF     // chunk data not followed by CRLF

	ErrHalfCRLF        = errors.New("chunked: stream ended between CR and LF")
	ErrTooManyTrailers = errors.New("chunked: trailer line count exceeds limit")
	ErrIncomplete      = errors.New("chunked: stream ended before message completed")
	ErrClosed          = errors.New("chunked: write after message completed")
	ErrChunkTooLarge   = errors.New("chunked: chunk size exceeds limit")
	ErrBodyTooLarge    = errors.New("chunked: body size exceeds limit")
)

// State identifies which part of the message the decoder is waiting
// for. It is reported in Error.State and by Decoder.State.
type State uint8

const (
	StateSizeLine  State = iota // inside a chunk-size line
	StateChunkData              // inside chunk data
	StateCRLF                   // waiting for the CRLF after chunk data
	StateTrailer                // inside the trailer section
	StateDone                   // message complete
	StateFailed                 // terminal error state
)

func (s State) String() string {
	switch s {
	case StateSizeLine:
		return "size-line"
	case StateChunkData:
		return "chunk-data"
	case StateCRLF:
		return "crlf"
	case StateTrailer:
		return "trailer"
	case StateDone:
		return "done"
	case StateFailed:
		return "failed"
	}
	return "unknown"
}

// Error is the single error type returned by the decoder.
type Error struct {
	Err    error // one of the sentinels above
	Offset int64 // absolute 0-based stream offset of the offending byte
	State  State // decoder state when the error was detected
}

func (e *Error) Error() string {
	return fmt.Sprintf("%v (stream offset %d, state %s)", e.Err, e.Offset, e.State)
}

// Unwrap returns the sentinel, so errors.Is(err, ErrBadSize) works.
func (e *Error) Unwrap() error { return e.Err }
