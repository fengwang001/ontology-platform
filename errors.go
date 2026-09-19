package ontology

import (
	"errors"
	"fmt"
)

// StepError records why the compensation of one step failed.
type StepError struct {
	Step int
	Err  error
}

func (e StepError) Error() string {
	return fmt.Sprintf("step %d compensation failed: %v", e.Step, e.Err)
}

func (e StepError) Unwrap() error { return e.Err }

// AggregateError bundles every compensation failure of one rollback.
// Each failing step appears at most once, keyed by its step number.
type AggregateError struct {
	failures []StepError
}

// Failures returns the failures in compensation execution order.
func (e *AggregateError) Failures() []StepError {
	out := make([]StepError, len(e.failures))
	copy(out, e.failures)
	return out
}

// FailureFor reports the failure recorded for step and whether one exists.
func (e *AggregateError) FailureFor(step int) (error, bool) {
	for _, f := range e.failures {
		if f.Step == step {
			return f.Err, true
		}
	}
	return nil, false
}

func (e *AggregateError) Error() string {
	return fmt.Sprintf("compensation failures (%d): %v", len(e.failures), e.failures)
}

// Is supports errors.Is(err, &AggregateError{}).
func (e *AggregateError) Is(target error) bool {
	_, ok := target.(*AggregateError)
	return ok
}

// PanicError wraps a recovered panic value from a compensation.
type PanicError struct {
	Value any
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("compensation panicked: %v", e.Value)
}

// Is supports errors.Is(err, &PanicError{}).
func (e *PanicError) Is(target error) bool {
	_, ok := target.(*PanicError)
	return ok
}

// AsPanic extracts a *PanicError from err.
func AsPanic(err error) (*PanicError, bool) {
	var pe *PanicError
	if errors.As(err, &pe) {
		return pe, true
	}
	return nil, false
}

// TaintedError is returned by every store write after pollution.
type TaintedError struct {
	// FirstTaintedStep is the smallest step number whose compensation failed.
	FirstTaintedStep int
}

func (e *TaintedError) Error() string {
	return fmt.Sprintf("store is tainted since step %d; writes are refused", e.FirstTaintedStep)
}

// Is supports errors.Is(err, &TaintedError{}).
func (e *TaintedError) Is(target error) bool {
	_, ok := target.(*TaintedError)
	return ok
}

// RollbackRejectedError is returned when rollback is invoked on a committed unit.
type RollbackRejectedError struct {
	Reason string
}

func (e *RollbackRejectedError) Error() string {
	return "rollback rejected: " + e.Reason
}

// Is supports errors.Is(err, &RollbackRejectedError{}).
func (e *RollbackRejectedError) Is(target error) bool {
	_, ok := target.(*RollbackRejectedError)
	return ok
}
