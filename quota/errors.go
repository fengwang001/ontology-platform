package quota

import (
	"errors"
	"fmt"
)

// 哨兵错误：所有被拒绝的操作返回的错误都可以用 errors.Is 与下列哨兵判定。
var (
	// ErrInvalidArgument 参数非法（负的 size、非正的预留量、小于 -1 的限额、空批等）。
	ErrInvalidArgument = errors.New("quota: invalid argument")
	// ErrNotFound 节点不存在。
	ErrNotFound = errors.New("quota: node not found")
	// ErrTypeMismatch 类型不符（期望目录得到文件，或反之）。
	ErrTypeMismatch = errors.New("quota: type mismatch")
	// ErrRoot 结构错误：Remove/Rename 作用于根目录。
	ErrRoot = errors.New("quota: operation not allowed on root")
	// ErrNotEmpty 结构错误：Remove 的目录非空。
	ErrNotEmpty = errors.New("quota: directory not empty")
	// ErrCycle 结构错误：Rename 把目录移入它自身或它的子孙之下。
	ErrCycle = errors.New("quota: rename into own subtree")
	// ErrInsufficientReservation 预留不足：Release 的 b 大于 r_d。
	ErrInsufficientReservation = errors.New("quota: insufficient reservation")
	// ErrByteQuotaExceeded 字节（有效字节）配额超限。
	ErrByteQuotaExceeded = errors.New("quota: byte quota exceeded")
	// ErrEntryQuotaExceeded 条目数配额超限。
	ErrEntryQuotaExceeded = errors.New("quota: entry quota exceeded")
	// ErrBelowUsage SetQuota 设置的限额低于该目录当前对应维度的用量。
	ErrBelowUsage = errors.New("quota: limit below current usage")
	// ErrBatchFailed 批处理失败（任一操作被拒绝，整批回滚）。
	ErrBatchFailed = errors.New("quota: batch failed")
)

// QuotaError 配额超限错误，携带违规目录编号与判定依据。
// 用 errors.Is 判定时，字节维度匹配 ErrByteQuotaExceeded，条目维度匹配 ErrEntryQuotaExceeded。
type QuotaError struct {
	Byte  bool  // true 为字节（有效字节）维度，false 为条目数维度
	Dir   int   // 违规目录编号
	Limit int64 // 该目录对应维度的限额
	Used  int64 // 当前用量（字节维度为有效字节 E）
	Delta int64 // 本次操作试图增加的量
}

func (e *QuotaError) Error() string {
	dim := "entry"
	if e.Byte {
		dim = "byte"
	}
	return fmt.Sprintf("quota: %s quota exceeded on dir %d: used %d + delta %d > limit %d",
		dim, e.Dir, e.Used, e.Delta, e.Limit)
}

// Is 使字节维度匹配 ErrByteQuotaExceeded，条目维度匹配 ErrEntryQuotaExceeded。
func (e *QuotaError) Is(target error) bool {
	if e.Byte {
		return target == ErrByteQuotaExceeded
	}
	return target == ErrEntryQuotaExceeded
}

// BelowUsageError SetQuota 限额低于当前用量，携带目录编号与维度。
type BelowUsageError struct {
	Dir   int
	Byte  bool
	Limit int64
	Used  int64
}

func (e *BelowUsageError) Error() string {
	dim := "entry"
	if e.Byte {
		dim = "byte"
	}
	return fmt.Sprintf("quota: %s limit %d below current usage %d on dir %d",
		dim, e.Limit, e.Used, e.Dir)
}

// Is 匹配 ErrBelowUsage。
func (e *BelowUsageError) Is(target error) bool { return target == ErrBelowUsage }

// BatchError 批处理失败错误：Index 为首个失败操作的下标，Err 为底层原因。
// errors.Is(err, ErrBatchFailed) 为真；errors.Is 同时可经 Unwrap 判出底层原因。
type BatchError struct {
	Index int
	Err   error
}

func (e *BatchError) Error() string {
	return fmt.Sprintf("quota: batch failed at op %d: %v", e.Index, e.Err)
}

// Unwrap 暴露底层失败原因。
func (e *BatchError) Unwrap() error { return e.Err }

// Is 匹配 ErrBatchFailed。
func (e *BatchError) Is(target error) bool { return target == ErrBatchFailed }

func notFoundErr(id int) error {
	return fmt.Errorf("%w: node %d", ErrNotFound, id)
}

func typeMismatchErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrTypeMismatch, fmt.Sprintf(format, args...))
}

func invalidArgErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidArgument, fmt.Sprintf(format, args...))
}
