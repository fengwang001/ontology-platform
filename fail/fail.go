// Package fail defines the terminal states of a task and the sentinel
// errors used to classify failures.
package fail

import (
	"errors"
	"fmt"
)

// Status is the terminal state of a task.
type Status int8

const (
	// Pending means the task has no terminal state yet.
	Pending Status = iota
	// Success means the task finished before any failure and returned nil.
	Success
	// Failed means the task ran and returned a real error or panicked.
	Failed
	// Skipped means the task never started because an ancestor failed.
	Skipped
	// Cancelled means the task was running when a failure aborted the run.
	Cancelled
)

func (s Status) String() string {
	switch s {
	case Success:
		return "success"
	case Failed:
		return "failed"
	case Skipped:
		return "skipped"
	case Cancelled:
		return "cancelled"
	}
	return "pending"
}

// Sentinel errors classifying terminal states; usable with errors.Is.
var (
	ErrFailed    = errors.New("task failed")
	ErrSkipped   = errors.New("task skipped")
	ErrCancelled = errors.New("task cancelled")
	ErrPanic     = errors.New("task panicked")
)

// PanicError wraps a recovered panic value from a task function.
type PanicError struct{ Value any }

func (e *PanicError) Error() string {
	return fmt.Sprintf("task panicked: %v", e.Value)
}

// Unwrap lets errors.Is(err, ErrPanic) match.
func (e *PanicError) Unwrap() error { return ErrPanic }
