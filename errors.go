package store

import "errors"

var (
	ErrInvalidArgument = errors.New("store: invalid argument")
	ErrLogGap          = errors.New("store: log gap")
	ErrIndexBehind     = errors.New("store: index is behind")
	ErrPrimaryNotFound = errors.New("store: primary key not found")
	ErrUniqueConflict  = errors.New("store: unique conflict")
	ErrSimulatedCrash  = errors.New("store: simulated crash before watermark commit")
)
