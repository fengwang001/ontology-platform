package growstack

import "errors"

// ErrorKind enumerates the fixed set of rejection categories.
type ErrorKind int

const (
	ErrUndefined ErrorKind = iota + 1
	ErrArgument
	ErrOverflow
	ErrQuota
	ErrDangling
	ErrCrossStack
	ErrEscape
	ErrConfig
)

func (k ErrorKind) String() string {
	switch k {
	case ErrUndefined:
		return "undefined"
	case ErrArgument:
		return "argument"
	case ErrOverflow:
		return "stack-overflow"
	case ErrQuota:
		return "quota-exhausted"
	case ErrDangling:
		return "dangling"
	case ErrCrossStack:
		return "cross-stack"
	case ErrEscape:
		return "escape"
	case ErrConfig:
		return "bad-config"
	default:
		return "unknown"
	}
}

// Error is a categorized subsystem error.
type Error struct {
	Kind ErrorKind
	Msg  string
}

func (e *Error) Error() string { return e.Kind.String() + ": " + e.Msg }

func kindOf(err error) ErrorKind {
	var se *Error
	if errors.As(err, &se) {
		return se.Kind
	}
	return 0
}

// Is reports whether err belongs to the given category.
func Is(err error, k ErrorKind) bool { return kindOf(err) == k }
