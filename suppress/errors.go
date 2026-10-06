package suppress

import "errors"

var (
	ErrInvalidArgument    = errors.New("invalid argument")
	ErrDuplicateDirective = errors.New("duplicate directive")
)
