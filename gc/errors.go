package gc

import (
	"errors"
	"fmt"
)

// Kind is the category of a rejected operation. Categories are ordered from
// the highest to the lowest precedence: when an operation violates several
// rules at once, the error with the highest precedence is reported and no
// state is changed.
type Kind int

const (
	// KindInvalidArgument: malformed input (empty ID, duplicate or self
	// owner reference, empty finalizer name, unknown policy).
	KindInvalidArgument Kind = iota
	// KindNotFound: the target object (or finalizer) does not exist.
	KindNotFound
	// KindConflict: the target object is being deleted (or already exists)
	// and the operation is not allowed in that state.
	KindConflict
	// KindCycle: the requested owner references would create a cycle.
	KindCycle
	// KindOwnerMissing: a referenced owner does not exist or is deleting.
	KindOwnerMissing
)

func (k Kind) String() string {
	switch k {
	case KindInvalidArgument:
		return "invalid argument"
	case KindNotFound:
		return "not found"
	case KindConflict:
		return "conflict"
	case KindCycle:
		return "cycle"
	case KindOwnerMissing:
		return "owner missing"
	}
	return "unknown"
}

// Error is the single error type returned by the controller.
type Error struct {
	Kind    Kind
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("gc: %s: %s", e.Kind, e.Message)
}

// KindOf extracts the category of an error returned by this package.
func KindOf(err error) (Kind, bool) {
	var ge *Error
	if errors.As(err, &ge) {
		return ge.Kind, true
	}
	return 0, false
}

func errorf(kind Kind, format string, args ...any) *Error {
	return &Error{Kind: kind, Message: fmt.Sprintf(format, args...)}
}
