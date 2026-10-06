package cg

import "fmt"

// Kind enumerates the distinguishable rejection classes.
type Kind int

const (
	OK Kind = iota
	InvalidArgument
	ClockBackward
	NotFound
	Conflict
	StateError
	DuplicateConfirm
	QueueFull
)

func (k Kind) String() string {
	switch k {
	case InvalidArgument:
		return "invalid_argument"
	case ClockBackward:
		return "clock_backward"
	case NotFound:
		return "not_found"
	case Conflict:
		return "conflict"
	case StateError:
		return "state_error"
	case DuplicateConfirm:
		return "duplicate_confirm"
	case QueueFull:
		return "queue_full"
	default:
		return "ok"
	}
}

// Error is a typed rejection; Kind is the ordered rejection class.
type Error struct {
	Kind    Kind
	Message string
}

func (e *Error) Error() string { return e.Kind.String() + ": " + e.Message }

// KindOf returns the rejection Kind of an error, or OK when err is nil.
// Non-cg errors map to -1.
func KindOf(err error) Kind {
	if err == nil {
		return OK
	}
	if e, ok := err.(*Error); ok {
		if e == nil {
			return OK
		}
		return e.Kind
	}
	return -1
}

func errf(k Kind, format string, args ...any) *Error {
	return &Error{Kind: k, Message: fmt.Sprintf(format, args...)}
}
