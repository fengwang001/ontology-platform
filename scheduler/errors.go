package scheduler

import "errors"

var (
	ErrInvalidConfig    = errors.New("scheduler: invalid configuration")
	ErrInvalidArgument  = errors.New("scheduler: invalid argument")
	ErrClockRollback    = errors.New("scheduler: clock moved backwards")
	ErrPodAlreadyExists = errors.New("scheduler: pod already exists")
	ErrPodNotFound      = errors.New("scheduler: pod not found")
	ErrPodNotInFlight   = errors.New("scheduler: pod is not in flight")
)
