package mcsched

import "errors"

// AddTask / SetDemand / Step 的拒绝原因。彼此可用 errors.Is 区分。
var (
	ErrStarted       = errors.New("mcsched: simulation already started")
	ErrInvalidParam  = errors.New("mcsched: invalid parameter")
	ErrDuplicateID   = errors.New("mcsched: duplicate task id")
	ErrDuplicatePrio = errors.New("mcsched: duplicate priority")
	ErrTaskTableFull = errors.New("mcsched: task table is full")
	ErrUnknownTask   = errors.New("mcsched: unknown task id")
	ErrJobReleased   = errors.New("mcsched: job already released")
)
