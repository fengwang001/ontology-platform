// Package upload tracks multipart upload registration and completion checks.
package upload

import "errors"

var (
	ErrBadPart      = errors.New("upload: bad part")
	ErrCompleted    = errors.New("upload: already completed")
	ErrMissingPart  = errors.New("upload: missing part")
	ErrEtagMismatch = errors.New("upload: etag mismatch")
)
