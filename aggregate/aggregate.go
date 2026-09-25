// Package aggregate collects every failure of a batch operation.
package aggregate

import (
	"errors"
	"strings"
)

// Aggregate is an indexable, ordered container of child errors.
// It has no single code of its own; Is/As reach the children.
type Aggregate []error

// New builds an Aggregate, dropping nil entries. It returns nil
// when no real error remains.
func New(errs ...error) error {
	kids := make(Aggregate, 0, len(errs))
	for _, e := range errs {
		if e != nil {
			kids = append(kids, e)
		}
	}
	if len(kids) == 0 {
		return nil
	}
	return kids
}

// Len returns the number of child errors.
func (a Aggregate) Len() int { return len(a) }

// At returns the child at index i (ordered as supplied).
func (a Aggregate) At(i int) error { return a[i] }

// All returns the children in aggregation order.
func (a Aggregate) All() []error { return []error(a) }

// Error joins child messages, one per line.
func (a Aggregate) Error() string {
	parts := make([]string, len(a))
	for i, e := range a {
		parts[i] = e.Error()
	}
	return strings.Join(parts, "; ")
}

// Is reports whether ANY child matches target, so callers can ask
// "did this batch contain a conflict / a permission failure?".
func (a Aggregate) Is(target error) bool {
	for _, e := range a {
		if errors.Is(e, target) {
			return true
		}
	}
	return false
}

// As assigns the first child that matches target's type.
func (a Aggregate) As(target any) bool {
	for _, e := range a {
		if errors.As(e, target) {
			return true
		}
	}
	return false
}

// Unwrap exposes every child (Go multi-error unwrap), integrating
// with errors.Is/errors.As/errors.Join tooling.
func (a Aggregate) Unwrap() []error { return []error(a) }

// Rebuild constructs an Aggregate from decoded children.
func Rebuild(kids []error) error {
	if len(kids) == 0 {
		return nil
	}
	return Aggregate(kids)
}
