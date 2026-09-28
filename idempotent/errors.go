package idempotent

import "fmt"

// ErrorCode 标识拒绝批次的具体原因。
type ErrorCode string

const (
	ErrNegativePartition ErrorCode = "negative_partition"
	ErrNegativeOffset    ErrorCode = "negative_offset"
	ErrEmptyKey          ErrorCode = "empty_key"
	ErrOutOfOrderOffset  ErrorCode = "out_of_order_offset"
	ErrTooManyPartitions ErrorCode = "too_many_partitions"
)

// ValidationError 描述一批输入被整体拒绝的原因。
type ValidationError struct {
	Code    ErrorCode
	Message string
	Index   int
}

func (e *ValidationError) Error() string {
	return string(e.Code) + ": " + e.Message
}

func reject(code ErrorCode, index int, format string, args ...any) error {
	return &ValidationError{Code: code, Index: index, Message: fmt.Sprintf(format, args...)}
}
