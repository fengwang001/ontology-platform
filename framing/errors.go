// Package framing implements a streaming length-prefixed frame reader.
package framing

import "errors"

// ErrFrameTooLarge is returned when a frame's length prefix exceeds the
// configured maximum. The reader enters a terminal failed state.
var ErrFrameTooLarge = errors.New("framing: frame too large")

// ErrIncomplete is returned by Close when the stream ends with a
// partially buffered frame.
var ErrIncomplete = errors.New("framing: incomplete frame at end of stream")

// ErrClosed is returned by Feed after the reader has been closed.
var ErrClosed = errors.New("framing: feed on closed reader")
