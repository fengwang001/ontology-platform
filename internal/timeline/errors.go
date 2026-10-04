package timeline

import "errors"

var (
	ErrBadArg     = errors.New("timeline: illegal argument")
	ErrClockBack  = errors.New("timeline: clock moved backwards")
	ErrDuplicate  = errors.New("timeline: id already exists")
	ErrPast       = errors.New("timeline: start is in the past")
	ErrOverlap    = errors.New("timeline: regular segments overlap")
	ErrOverride   = errors.New("timeline: override segments conflict")
	ErrInFixed    = errors.New("timeline: shift starts inside a fixed program")
	ErrCrowdFixed = errors.New("timeline: shift would crowd a fixed program")
	ErrNotFound   = errors.New("timeline: id not found")
	ErrEnded      = errors.New("timeline: program already ended")
	ErrBadTime    = errors.New("timeline: query time out of range")
)
