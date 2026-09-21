package upload

import "errors"

var (
	ErrBadPart      = errors.New("upload: bad part number or size")
	ErrCompleted    = errors.New("upload: already completed")
	ErrMissingPart  = errors.New("upload: missing parts")
	ErrEtagMismatch = errors.New("upload: etag mismatch")
)
