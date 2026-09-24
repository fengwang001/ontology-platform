// Package fail defines task lifecycle states, sentinel errors and the
// propagation error types used across the scheduler.
package fail

import (
	"errors"
	"fmt"
)

// State is the final (or transient) lifecycle state of a task.
type State int

const (
	Pending State = iota
	Running
	Successed
	Failed
	Skipped
	Canceled
)

// String returns the stable, report-facing name of the state.
func (s State) String() string {
	switch s {
	case Pending:
		return "PENDING"
	case Running:
		return "RUNNING"
	case Successed:
		return "SUCCESSED"
	case Failed:
		return "FAILED"
	case Skipped:
		return "SKIPPED"
	case Canceled:
		return "CANCELED"
	default:
		return "UNKNOWN"
	}
}

var (
	// ErrCyclic indicates that the graph contains a cycle.
	ErrCyclic = errors.New("graph contains a cycle")
	// ErrNoSuchTask indicates a reference (edge or function) to an unknown task.
	ErrNoSuchTask = errors.New("task does not exist")
)

// SkipError marks a task as skipped because an upstream task failed.
// Origin is the deterministic "earliest" failed ancestor (smallest ID).
type SkipError struct {
	Origin string
}

func (e *SkipError) Error() string {
	return "skipped due to failure of task " + e.Origin
}

// PanicError wraps a value recovered from a panicking task function.
type PanicError struct {
	Value any
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("task panicked: %v", e.Value)
}

// CanceledError marks a running task whose context was canceled by
// fail-fast propagation.
type CanceledError struct{}

func (e *CanceledError) Error() string { return "task canceled due to another failure" }

func (e *CanceledError) Is(target error) bool {
	_, ok := target.(*CanceledError)
	return ok
}
