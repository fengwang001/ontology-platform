package upload

import "errors"

var (
	// ErrBadPart 分片号非法或大小不合规。
	ErrBadPart = errors.New("upload: bad part")
	// ErrCompleted 已完成后再改动。
	ErrCompleted = errors.New("upload: already completed")
	// ErrMissingPart 完成时有缺片。
	ErrMissingPart = errors.New("upload: missing part")
	// ErrEtagMismatch 完成时提供的校验值对不上。
	ErrEtagMismatch = errors.New("upload: etag mismatch")
)
