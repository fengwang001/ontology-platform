package compaction

import "errors"

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrFileNotFound    = errors.New("file not found")
	ErrFileOccupied    = errors.New("file occupied")
	ErrPlanNotFound    = errors.New("plan not found")
	ErrLayerInvariant  = errors.New("layer invariant violated")
)
