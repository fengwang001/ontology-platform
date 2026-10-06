package pvbinding

import "fmt"

type ErrorCode int

const (
	ErrCodeInvalidArgument ErrorCode = iota + 1
	ErrCodeNotFound
	ErrCodeConflict
	ErrCodeNoMatchingVolume
	ErrCodeInsufficientCapacity
)

type Error struct {
	Code    ErrorCode
	Message string
}

func (e Error) Error() string {
	return e.Message
}

func bindingError(code ErrorCode, format string, args ...any) Error {
	return Error{Code: code, Message: fmt.Sprintf(format, args...)}
}
