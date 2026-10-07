package bitemporal

import "fmt"

// ErrorKind 是查询入参校验错误的四类互斥类别，按固定次序判定并只报第一类。
type ErrorKind int

const (
	// ErrSourceNotFound：源对象实例在存储中完全不存在（无任何写入记录）。
	ErrSourceNotFound ErrorKind = iota + 1
	// ErrInvalidTime：有效时间点或写入时间点为非法值（零值）。
	ErrInvalidTime
	// ErrInvalidDepth：遍历深度上限不是正整数。
	ErrInvalidDepth
	// ErrAsOfBeforeEarliestWrite：写入时间点早于源对象自身最早的写入时间。
	ErrAsOfBeforeEarliestWrite
)

// QueryError 携带固定判定次序的校验错误类别。
type QueryError struct {
	Kind  ErrorKind
	Field string
}

func (e *QueryError) Error() string {
	return fmt.Sprintf("bitemporal query error (kind=%d, field=%s)", e.Kind, e.Field)
}

// WriteError 表示追加写入违反了写入时间单调严格递增等约束。
type WriteError struct {
	Message string
}

func (e *WriteError) Error() string { return e.Message }
