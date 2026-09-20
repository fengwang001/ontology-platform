package headers

import "errors"

var (
	ErrMalformed   error = errors.New("headers: malformed message header")
	ErrSingleValue error = errors.New("headers: single-value header appears multiple times with different values")
)
