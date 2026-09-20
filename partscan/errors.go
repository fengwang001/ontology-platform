package partscan

import "errors"

var (
	// ErrNoPreamble means the stream did not begin with "--<boundary>\r\n".
	ErrNoPreamble = errors.New("partscan: stream does not start with boundary preamble")
	// ErrIncomplete is returned by Close when the closing delimiter was never seen.
	ErrIncomplete = errors.New("partscan: stream ended before closing delimiter")
	// ErrAfterClose is returned when data is fed after the closing delimiter.
	ErrAfterClose = errors.New("partscan: data fed after closing delimiter")
)
