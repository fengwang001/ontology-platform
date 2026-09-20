// Package byterange parses HTTP Range headers and normalizes the
// requested byte ranges against a known resource size.
package byterange

import "errors"

// ErrMalformed reports a syntactically invalid Range header
// (bad unit, missing '=', non-numeric bounds, start > end, ...).
var ErrMalformed = errors.New("byterange: malformed range header")

// ErrUnsatisfiable reports a syntactically valid header in which
// no requested range can be satisfied for the given resource size.
var ErrUnsatisfiable = errors.New("byterange: no satisfiable range")
