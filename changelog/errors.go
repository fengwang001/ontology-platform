package changelog

import "errors"

var (
	errEmptyKey      = errors.New("changelog: key must not be empty")
	errPositionBelow = errors.New("changelog: position must be >= 1")
	errPositionAbove = errors.New("changelog: position must be <= next sequence")
	errRangeLeft     = errors.New("changelog: left must be >= 1")
	errRangeRight    = errors.New("changelog: right must be < next sequence")
	errRangeOrder    = errors.New("changelog: left must be <= right")
)
