package scaler

import "errors"

var (
	ErrInvalidConfig     = errors.New("invalid scaler config")
	ErrInvalidNode       = errors.New("invalid node")
	ErrNodeExists        = errors.New("node already exists")
	ErrInvalidPod        = errors.New("invalid pod")
	ErrPodExists         = errors.New("pod already exists")
	ErrNodeNotFound      = errors.New("node not found")
	ErrInsufficientSpace = errors.New("insufficient node capacity")
	ErrPodNotFound       = errors.New("pod not found")
	ErrInvalidTime       = errors.New("invalid time")
	ErrClockRollback     = errors.New("clock rollback")
)
