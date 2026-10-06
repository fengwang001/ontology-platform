package scholarship

import "fmt"

// ErrorCode 标识可区分的错误类别，固定优先级见各常量。
type ErrorCode int

const (
	ErrInvalidArgument ErrorCode = iota + 1
	ErrNotFound
	ErrNotEvaluated
	ErrAwardNotInResult
	ErrAlreadyConfirmed
)

// EngineError 携带固定优先级的错误码。
type EngineError struct {
	Code ErrorCode
	Msg  string
}

func (e *EngineError) Error() string { return e.Msg }

func errf(code ErrorCode, format string, args ...any) error {
	return &EngineError{Code: code, Msg: fmt.Sprintf(format, args...)}
}
