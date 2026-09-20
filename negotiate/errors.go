// Package negotiate implements HTTP Accept header content negotiation:
// it picks the most suitable media type from a server's supported offers
// according to the semantics of the Accept header.
package negotiate

import "errors"

// ErrMalformed is returned when the Accept header has a syntax error,
// such as a missing slash, an empty type or subtype, a parameter
// without '=', or an invalid q value.
var ErrMalformed = errors.New("negotiate: malformed Accept header")

// ErrNotAcceptable is returned when no offer matches any acceptable
// Accept rule (or offers is empty).
var ErrNotAcceptable = errors.New("negotiate: no acceptable media type")
