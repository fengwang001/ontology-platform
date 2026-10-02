package gc

import "errors"

var (
	ErrInvalidArgument = errors.New("gc: invalid argument")
	ErrInvalidConfig   = errors.New("gc: invalid config")
	ErrClockSkew       = errors.New("gc: clock moved backwards")
	ErrImageExists     = errors.New("gc: image already exists")
	ErrImageNotFound   = errors.New("gc: image not found")
	ErrImagePulling    = errors.New("gc: image is pulling")
	ErrImageNotPulling = errors.New("gc: image is not pulling")
	ErrNoSpace         = errors.New("gc: insufficient disk space")
	ErrNotRunning      = errors.New("gc: image is not running")
)

type LayerConflictError struct {
	LayerID string
}

func (e *LayerConflictError) Error() string {
	return "gc: layer conflict: " + e.LayerID
}
