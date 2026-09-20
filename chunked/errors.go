// Package chunked implements the write side of chunked transfer encoding:
// it encodes writes into a sequence of "<hex length>\r\n<data>\r\n" blocks
// terminated by a final "0\r\n\r\n" block.
package chunked

import "errors"

// ErrClosed is returned by Write after the writer has been closed.
var ErrClosed = errors.New("chunked: write after close")

// ErrSinkFail is returned (wrapped, together with the underlying sink
// error) once the underlying io.Writer has failed. It is sticky: after
// the first sink failure every subsequent Write and Close reports it.
var ErrSinkFail = errors.New("chunked: sink failed")
