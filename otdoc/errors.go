package otdoc

import "errors"

var (
	ErrInvalid  = errors.New("otdoc: invalid argument")
	ErrStaleSeq = errors.New("otdoc: stale sequence number")
	ErrSeqGap   = errors.New("otdoc: sequence number gap")
	ErrFuture   = errors.New("otdoc: base revision is in the future")
	ErrTooOld   = errors.New("otdoc: base revision is older than the floor")
	ErrLength   = errors.New("otdoc: operation base length does not match document length at base revision")
	ErrTooLarge = errors.New("otdoc: transformed document would exceed max length")
	ErrBadFloor = errors.New("otdoc: bad floor")
	ErrBadRange = errors.New("otdoc: bad history range")
)
