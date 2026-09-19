package topk

import "errors"

// ErrInvalidK is returned by New when the capacity is not positive.
var ErrInvalidK = errors.New("topk: K must be greater than zero")
