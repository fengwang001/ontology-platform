// Package exec runs individual task functions, converting panics and
// context cancellation into ordinary errors.
package exec

import (
	"context"

	"ontology/fail"
)

// Func is a unit of work. The context is canceled by fail-fast
// propagation; a cooperative task should return promptly when Done fires.
type Func func(ctx context.Context) error

// Run executes fn with panic recovery. Behavior:
//   - if ctx is already canceled, the task is treated as Canceled;
//   - a panic becomes *fail.PanicError carrying the original value;
//   - otherwise fn's error (possibly nil) is returned unchanged.
//
// A task that ignores cancellation still runs to completion; the scheduler
// is responsible for discarding its late result.
func Run(ctx context.Context, fn Func) (err error) {
	if err := ctx.Err(); err != nil {
		return &fail.CanceledError{}
	}
	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- &fail.PanicError{Value: r}
			}
		}()
		done <- fn(ctx)
	}()
	select {
	case <-ctx.Done():
		// The function may still be running and may still send on done;
		// the buffered slot absorbs that late write so no goroutine leaks.
		return &fail.CanceledError{}
	case err := <-done:
		return err
	}
}
