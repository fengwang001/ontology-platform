// Package chunked implements the write side of HTTP chunked transfer encoding.
//
// It encodes successive writes as a sequence of chunks of the form
// "<hex-size>\r\n<data>\r\n" and terminates the stream with "0\r\n\r\n".
// Only the encoding (write) side is provided; there is no reader, trailer,
// compression, or HTTP support.
package chunked

import "errors"

// ErrClosed is returned when Write is called after Close.
var ErrClosed = errors.New("chunked: writer already closed")

// ErrSinkFail wraps any failure reported by the underlying io.Writer.
// A sink failure is sticky: once it occurs, later Write and Close calls
// return an error that matches ErrSinkFail via errors.Is and never touch
// the sink again. Use errors.As or errors.Unwrap to reach the original
// sink error.
var ErrSinkFail = errors.New("chunked: underlying writer failed")

// sinkError joins ErrSinkFail with the original error returned by the sink,
// so errors.Is(err, ErrSinkFail) succeeds while errors.Is/errors.As can still
// reach the sink's own error.
type sinkError struct {
	cause error
}

func (e *sinkError) Error() string {
	return ErrSinkFail.Error() + ": " + e.cause.Error()
}

// Unwrap exposes both the sentinel and the original sink error.
func (e *sinkError) Unwrap() []error {
	return []error{ErrSinkFail, e.cause}
}

// fail remembers a sink failure on w and returns the sticky error.
func (w *Writer) fail(cause error) error {
	if w.sinkErr == nil {
		w.sinkErr = &sinkError{cause: cause}
	}
	return w.sinkErr
}
