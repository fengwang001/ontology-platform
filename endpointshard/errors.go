package endpointshard

import (
	"errors"
	"fmt"
)

// ErrorKind classifies rejected operations. Kinds are listed in
// descending priority: when several kinds could apply to one call, the
// kind with the smallest numeric value is reported and the operation
// leaves all state untouched.
type ErrorKind int

const (
	// ErrInvalidArgument indicates malformed input (empty endpoint ID,
	// empty region, duplicate ID, non-positive capacity, ...).
	ErrInvalidArgument ErrorKind = iota + 1
	// ErrServiceNotFound indicates the named service does not exist.
	ErrServiceNotFound
	// ErrServiceAlreadyExists indicates the named service already exists.
	ErrServiceAlreadyExists
)

func (k ErrorKind) String() string {
	switch k {
	case ErrInvalidArgument:
		return "invalid argument"
	case ErrServiceNotFound:
		return "service not found"
	case ErrServiceAlreadyExists:
		return "service already exists"
	default:
		return fmt.Sprintf("unknown error kind %d", int(k))
	}
}

// Error is the single error type returned by this package. Use KindOf
// to inspect the category.
type Error struct {
	Kind    ErrorKind
	Op      string
	Service string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("endpointshard: %s: %s: service %q: %s", e.Op, e.Kind, e.Service, e.Message)
}

// KindOf extracts the ErrorKind from an error returned by this package.
func KindOf(err error) (ErrorKind, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind, true
	}
	return 0, false
}

func invalidArg(op, service, format string, args ...any) *Error {
	return &Error{Kind: ErrInvalidArgument, Op: op, Service: service, Message: fmt.Sprintf(format, args...)}
}

func notFound(op, service string) *Error {
	return &Error{Kind: ErrServiceNotFound, Op: op, Service: service, Message: "service does not exist"}
}

func alreadyExists(op, service string) *Error {
	return &Error{Kind: ErrServiceAlreadyExists, Op: op, Service: service, Message: "service already exists"}
}
