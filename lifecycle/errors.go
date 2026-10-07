package lifecycle

import (
	"errors"
	"fmt"
)

// Error categories with fixed priority. Lower number = higher priority.
//
// (1) ErrExpiryGuard        - an expiry link in the lazy settlement chain
//
//	has a non-temporal precondition that fails.
//
// (2) ErrChainTrigger       - a cross-instance chained effect failed.
// (3) ErrActionPrecondition - the explicit action is not enabled after
//
//	expiry settlement.
//
// (4) ErrClockRegression    - the observed clock moved backwards.
type ErrorCategory int

const (
	CategoryExpiryGuard ErrorCategory = iota + 1
	CategoryChainTrigger
	CategoryActionPrecondition
	CategoryClockRegression
)

// LifecycleError is implemented by every error reported by the engine.
type LifecycleError interface {
	error
	Category() ErrorCategory
}

type lifecycleError struct {
	cat ErrorCategory
	msg string
}

func (e *lifecycleError) Error() string           { return e.msg }
func (e *lifecycleError) Category() ErrorCategory { return e.cat }

// Named sentinel errors. Concrete failures wrap these, so errors.Is works.
var (
	ErrExpiryGuard = &lifecycleError{
		cat: CategoryExpiryGuard,
		msg: "lifecycle: expiry settlement stopped: non-temporal precondition not satisfied",
	}
	ErrChainTrigger = &lifecycleError{
		cat: CategoryChainTrigger,
		msg: "lifecycle: cross-instance chained expiry effect failed",
	}
	ErrActionPrecondition = &lifecycleError{
		cat: CategoryActionPrecondition,
		msg: "lifecycle: explicit action precondition not satisfied after expiry settlement",
	}
	ErrClockRegression = &lifecycleError{
		cat: CategoryClockRegression,
		msg: "lifecycle: clock regression observed during settlement",
	}
)

// Failure carries a fixed-priority category plus contextual detail.
type Failure struct {
	base *lifecycleError
	why  string
}

func fail(base *lifecycleError, format string, args ...any) *Failure {
	return &Failure{base: base, why: fmt.Sprintf(format, args...)}
}

func (f *Failure) Error() string {
	if f.why == "" {
		return f.base.Error()
	}
	return f.base.Error() + ": " + f.why
}

// Category reports the fixed-priority error class.
func (f *Failure) Category() ErrorCategory { return f.base.cat }

// Unwrap exposes the sentinel so errors.Is(err, ErrExpiryGuard) works.
func (f *Failure) Unwrap() error { return f.base }

// CategoryOf returns the error category and true for lifecycle failures.
func CategoryOf(err error) (ErrorCategory, bool) {
	var le LifecycleError
	if errors.As(err, &le) {
		return le.Category(), true
	}
	return 0, false
}

// HigherPriority reports whether err outranks other (smaller category).
func HigherPriority(err, other error) bool {
	a, ok1 := CategoryOf(err)
	b, ok2 := CategoryOf(other)
	if ok1 != ok2 {
		return ok1
	}
	return ok1 && a < b
}
