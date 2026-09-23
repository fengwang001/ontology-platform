package res

import (
	"context"
	"errors"

	"ontology/op"
)

// OpenChild opens a child during a parent's Open. On failure it closes the
// already-open children in reverse order and returns the joined error, so
// every opened child is rolled back.
func OpenChild(ctx context.Context, opened *[]op.Operator, child op.Operator) error {
	if err := child.Open(ctx); err != nil {
		var errs []error
		for i := len(*opened) - 1; i >= 0; i-- {
			errs = append(errs, (*opened)[i].Close())
		}
		*opened = (*opened)[:0]
		return errors.Join(append([]error{err}, errs...)...)
	}
	*opened = append(*opened, child)
	return nil
}

// Run executes one volcano tree to completion (or first error), drains rows
// into the returned slice, and always closes the root exactly once. Close
// errors are joined with the first Next error: they never suppress each
// other and never abort the rest of the top-down close chain.
func Run(ctx context.Context, root op.Operator) ([]op.Row, error) {
	if err := root.Open(ctx); err != nil {
		return nil, err
	}

	rows, nextErr := op.Drain(ctx, root)

	closeErr := root.Close()
	return rows, errors.Join(nextErr, closeErr)
}

// CloseAll closes the given operators in reverse order and joins all
// non-nil errors without stopping.
func CloseAll(ops ...op.Operator) error {
	var errs []error
	for i := len(ops) - 1; i >= 0; i-- {
		if err := ops[i].Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
