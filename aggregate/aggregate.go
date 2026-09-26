// Package aggregate collects every error produced by a batch operation.
package aggregate

import (
	"errors"
	"strings"
)

// Aggregate is an ordered container of child errors. It is indexable and
// traversable, and implements the multi-error Unwrap protocol so that
// errors.Is matches when ANY child matches: callers can still tell whether a
// batch contained, say, a conflict.
type Aggregate struct {
	errs []error
}

// New builds an Aggregate from the given children, preserving order.
// Nil entries are dropped; no children yields nil.
func New(errs ...error) *Aggregate {
	kept := make([]error, 0, len(errs))
	for _, err := range errs {
		if err != nil {
			kept = append(kept, err)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return &Aggregate{errs: kept}
}

// Len returns the number of aggregated child errors.
func (a *Aggregate) Len() int {
	if a == nil {
		return 0
	}
	return len(a.errs)
}

// At returns the child at index i and ok=true, or nil,false when out of range.
func (a *Aggregate) At(i int) (error, bool) {
	if a == nil || i < 0 || i >= len(a.errs) {
		return nil, false
	}
	return a.errs[i], true
}

// Range visits children in aggregation order. Returning false stops iteration.
func (a *Aggregate) Range(fn func(i int, err error) bool) {
	if a == nil {
		return
	}
	for i, err := range a.errs {
		if !fn(i, err) {
			return
		}
	}
}

// Unwrap exposes all children for the standard errors.Is/As multi-error walk.
func (a *Aggregate) Unwrap() []error {
	if a == nil {
		return nil
	}
	return a.errs
}

// Error joins child messages in order, one per line.
func (a *Aggregate) Error() string {
	if a == nil || len(a.errs) == 0 {
		return ""
	}
	var b strings.Builder
	for i, err := range a.errs {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(err.Error())
	}
	return b.String()
}

// Is reports whether any child matches target. It is a thin, explicit
// expression of the "match any child" rule; the standard library achieves the
// same via Unwrap() []error.
func (a *Aggregate) Is(target error) bool {
	if a == nil {
		return false
	}
	for _, err := range a.errs {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

// As assigns the first child (in order) assignable to target.
func (a *Aggregate) As(target any) bool {
	if a == nil {
		return false
	}
	for _, err := range a.errs {
		if errors.As(err, target) {
			return true
		}
	}
	return false
}
