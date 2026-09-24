// Package exec runs task functions and collects their outcomes.
package exec

import (
	"context"
	"errors"
	"fmt"
)

// ErrPanic is matched (via errors.Is) when a task panics.
var ErrPanic = errors.New("exec: task panicked")

// PanicError wraps a recovered panic value.
type PanicError struct{ Value any }

func (e *PanicError) Error() string { return fmt.Sprintf("%s: %v", ErrPanic, e.Value) }

func (e *PanicError) Is(target error) bool { return target == ErrPanic }

// Func is a task body; ctx is canceled when the scheduler gives up on it.
type Func func(ctx context.Context) error

// Result is one task's outcome, collected by the scheduler.
type Result struct {
	ID  string
	Err error
}

// Run invokes fn, converting a panic into a *PanicError failure.
func Run(ctx context.Context, fn Func) (err error) {
	if fn == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			err = &PanicError{Value: r}
		}
	}()
	return fn(ctx)
}
