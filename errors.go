package rollback

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Sentinel errors. They are compared through the standard errors.Is chain.
var (
	// ErrPolluted is returned by every mutating store call once the store
	// has been polluted by a failed compensation.
	ErrPolluted = errors.New("rollback: store is polluted")

	// ErrRolledBack is returned when Rollback is invoked after the unit has
	// already rolled back.
	ErrRolledBack = errors.New("rollback: unit already rolled back")

	// ErrCommitted is returned when Rollback is invoked after a successful
	// commit; compensations are never replayed.
	ErrCommitted = errors.New("rollback: unit already committed")

	// ErrInProgress is returned when an execution races with an active rollback.
	ErrInProgress = errors.New("rollback: rollback already in progress")
)

// PollutionError wraps ErrPolluted and carries the step number of the
// earliest pollution point (0 when unknown).
type PollutionError struct {
	Step int
}

func (e *PollutionError) Error() string {
	return fmt.Sprintf("%v (pollution first introduced at step %d)", ErrPolluted, e.Step)
}

func (e *PollutionError) Unwrap() error { return ErrPolluted }

// StepError records a single compensation failure for one step.
type StepError struct {
	Step int
	Err  error
}

func (e *StepError) Error() string {
	return fmt.Sprintf("step %d: %v", e.Step, e.Err)
}

func (e *StepError) Unwrap() error { return e.Err }

// CompensationPanic is the error produced when a compensation panics. The
// recovered panic value is available through Value and errors.Is/As.
type CompensationPanic struct {
	Step  int
	Value any
}

func (e *CompensationPanic) Error() string {
	return fmt.Sprintf("step %d: compensation panicked: %v", e.Step, e.Value)
}

// AggregateError is the single error returned by a rollback when one or more
// compensations failed (including panics). Failures are looked up by step
// number through Step / Failures.
type AggregateError struct {
	failures map[int]error
}

func newAggregateError(failures map[int]error) *AggregateError {
	return &AggregateError{failures: failures}
}

// Step returns the failure reason for step, or nil when that step
// compensated successfully (or never ran).
func (e *AggregateError) Step(step int) error {
	if e == nil {
		return nil
	}
	return e.failures[step]
}

// Failures returns the failure steps in ascending order.
func (e *AggregateError) Failures() []StepError {
	if e == nil {
		return nil
	}
	steps := make([]int, 0, len(e.failures))
	for step := range e.failures {
		steps = append(steps, step)
	}
	sort.Ints(steps)
	out := make([]StepError, 0, len(steps))
	for _, step := range steps {
		out = append(out, StepError{Step: step, Err: e.failures[step]})
	}
	return out
}

func (e *AggregateError) Error() string {
	failures := e.Failures()
	parts := make([]string, 0, len(failures)+1)
	parts = append(parts, fmt.Sprintf("rollback: %d compensation(s) failed:", len(failures)))
	for _, failure := range failures {
		parts = append(parts, "  "+failure.Error())
	}
	return strings.Join(parts, "\n")
}

// ExecutionError reports the failure of a forward action. It carries the
// step number and the original action error.
type ExecutionError struct {
	Step int
	Err  error
}

func (e *ExecutionError) Error() string {
	return fmt.Sprintf("step %d failed: %v", e.Step, e.Err)
}

func (e *ExecutionError) Unwrap() error { return e.Err }

// ActionPanic is produced when a forward action panics.
type ActionPanic struct {
	Step  int
	Value any
}

func (e *ActionPanic) Error() string {
	return fmt.Sprintf("step %d: action panicked: %v", e.Step, e.Value)
}
