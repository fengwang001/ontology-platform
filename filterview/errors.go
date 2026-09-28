package filterview

import (
	"errors"
	"fmt"
)

// 各类拒绝原因，调用方可用 errors.Is 精确区分。
var (
	// ErrInvalidInterval 表示 low >= high，过滤区间非法。
	ErrInvalidInterval = errors.New("filterview: invalid interval: low must be less than high")
	// ErrEmptyKey 表示输入行的主键为空。
	ErrEmptyKey = errors.New("filterview: empty row key")
	// ErrUnknownOp 表示操作类型未知。
	ErrUnknownOp = errors.New("filterview: unknown operation kind")
	// ErrUpdateKeyMismatch 表示更新前后主键不同。
	ErrUpdateKeyMismatch = errors.New("filterview: update before/after key mismatch")
	// ErrDuplicateKey 表示插入的主键在源表中已存在，或同一批内主键冲突。
	ErrDuplicateKey = errors.New("filterview: duplicate key")
	// ErrKeyNotFound 表示删除/更新的主键在源表中不存在。
	ErrKeyNotFound = errors.New("filterview: key not found")
	// ErrBeforeMismatch 表示前像与源表当前行不逐字段相等。
	ErrBeforeMismatch = errors.New("filterview: before image does not match current source row")
)

// RejectError 携带拒绝发生的批内位置（从 0 起）与具体原因。
type RejectError struct {
	Index int
	Op    Op
	Err   error
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("filterview: batch rejected at op %d: %v", e.Index, e.Err)
}

func (e *RejectError) Unwrap() error { return e.Err }
