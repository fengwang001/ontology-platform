package ontology

import (
	"errors"
	"fmt"
)

// ErrorCode 以可区分的枚举标识拒绝原因，调用方可据此分类处理。
type ErrorCode int

const (
	// ErrNone 表示无错误（零值占位）。
	ErrNone ErrorCode = iota
	// ErrInvalidInitialColumns 建表列定义非法（空表 / 重名 / 空列名）。
	ErrInvalidInitialColumns
	// ErrVersionNotFound 事件或查询引用了不存在的版本号。
	ErrVersionNotFound
	// ErrValueCountMismatch 事件值个数与所在版本的列数不符。
	ErrValueCountMismatch
	// ErrInvalidChange 演进操作非法（未知类型、列不存在、空名、重名、
	// 对已删除列操作、同批重复等）。
	ErrInvalidChange
	// ErrTooManyVersions 版本数超过上限。
	ErrTooManyVersions
	// ErrInvalidArgument 其它入参非法（nil 等）。
	ErrInvalidArgument
)

// String 返回错误码的稳定短名，便于日志与断言。
func (c ErrorCode) String() string {
	switch c {
	case ErrNone:
		return "none"
	case ErrInvalidInitialColumns:
		return "invalid_initial_columns"
	case ErrVersionNotFound:
		return "version_not_found"
	case ErrValueCountMismatch:
		return "value_count_mismatch"
	case ErrInvalidChange:
		return "invalid_change"
	case ErrTooManyVersions:
		return "too_many_versions"
	case ErrInvalidArgument:
		return "invalid_argument"
	default:
		return fmt.Sprintf("unknown_error_code(%d)", int(c))
	}
}

// DecodeError 携带可区分的错误码与上下文信息。
type DecodeError struct {
	// Code 为机器可读的错误码。
	Code ErrorCode
	// Message 为人类可读的说明。
	Message string
}

func (e *DecodeError) Error() string {
	if e == nil {
		return "ontology: nil error"
	}
	if e.Message != "" {
		return "ontology/" + e.Code.String() + ": " + e.Message
	}
	return "ontology/" + e.Code.String()
}

// newError 构造一个 DecodeError。
func newError(code ErrorCode, format string, args ...any) *DecodeError {
	return &DecodeError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// AsErrorCode 从任意 error 中提取 ErrorCode；不是 DecodeError 时返回 ErrNone。
func AsErrorCode(err error) ErrorCode {
	var de *DecodeError
	if errors.As(err, &de) {
		return de.Code
	}
	return ErrNone
}
