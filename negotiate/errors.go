package negotiate

import "errors"

var (
	ErrMalformed     = errors.New("negotiate: malformed Accept header")
	ErrNotAcceptable = errors.New("negotiate: no acceptable media type")
)
