package catalog

import "errors"

var (
	ErrInvalidCode  = errors.New("catalog: invalid code")
	ErrInvalidClass = errors.New("catalog: invalid class")
	ErrInvalidP     = errors.New("catalog: invalid prepay percent")
	ErrInvalidLimit = errors.New("catalog: invalid limit")
)
