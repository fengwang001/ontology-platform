package pipeline

import "errors"

var (
	errCrashInjected = errors.New("pipeline: crash injected")
	errCountMismatch = errors.New("pipeline: merged count mismatch")
	errRunMismatch   = errors.New("pipeline: run recovery count mismatch")
)
