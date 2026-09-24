// Package exec runs individual task functions and reports outcome events.
package exec

import (
	"context"
	"fmt"
)

// Outcome describes how a task function returned.
type Outcome int

const (
	// Done means the task returned nil before cancellation.
	Done Outcome = iota
	// Failed means the task returned a non-nil error.
	Failed
	// Panicked means the task panicked; Err carries the recovered value.
	Panicked
	// Canceled means the context was canceled before the task finished.
	Canceled
)

// Event is the single result of one task execution.
type Event struct {
	ID      string
	Outcome Outcome
	Err     error
}

// TaskFunc is a user-supplied task body.
type TaskFunc func(ctx context.Context) error

// Executor launches tasks as goroutines and funnels their events onto Events.
type Executor struct {
	Events chan Event
}

// New creates an Executor with a buffered event channel.
func New(buffer int) *Executor {
	if buffer < 1 {
		buffer = 1
	}
	return &Executor{Events: make(chan Event, buffer)}
}

// Run starts one task. The goroutine always sends exactly one Event and exits:
// panics are recovered, and context cancellation preempts a still-running task.
// A task that ignores cancellation keeps running; its late event is still sent
// and is safely discarded by the engine.
func (e *Executor) Run(id string, fn TaskFunc, ctx context.Context) {
	go func() {
		res := make(chan error, 1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					res <- &PanicError{Value: r}
				}
			}()
			res <- fn(ctx)
		}()
		select {
		case err := <-res:
			switch {
			case err == nil:
				e.Events <- Event{ID: id, Outcome: Done}
			default:
				if _, ok := err.(*PanicError); ok {
					e.Events <- Event{ID: id, Outcome: Panicked, Err: err}
				} else {
					e.Events <- Event{ID: id, Outcome: Failed, Err: err}
				}
			}
		case <-ctx.Done():
			e.Events <- Event{ID: id, Outcome: Canceled, Err: context.Canceled}
		}
	}()
}

// PanicError marks a failure caused by a recovered panic.
type PanicError struct{ Value any }

func (e *PanicError) Error() string {
	return fmt.Sprintf("[panic] %v", e.Value)
}

// IsPanic reports whether err wraps a task panic.
func IsPanic(err error) bool {
	_, ok := err.(*PanicError)
	return ok
}
