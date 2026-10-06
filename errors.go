package ontology

import "fmt"

type ErrorCode string

const (
	ErrUndefined  ErrorCode = "undefined"
	ErrParameters ErrorCode = "parameters"
	ErrConstraint ErrorCode = "constraint"
	ErrDepth      ErrorCode = "depth"
	ErrQuota      ErrorCode = "quota"
	ErrDependency ErrorCode = "dependency"
	ErrDefinition ErrorCode = "definition"
)

type Error struct {
	Code    ErrorCode
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func errorf(code ErrorCode, format string, args ...any) error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}
