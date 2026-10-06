// Package remittance 实现跨境汇款的报价锁汇、额度占用与合规审核。
package remittance

import "fmt"

// ErrorCode 以可区分的编号标识全部业务错误。
type ErrorCode int

const (
	// ErrCodeInvalidArgument 参数非法（含未知汇款人、空字段、非正金额/汇率等）。
	ErrCodeInvalidArgument ErrorCode = iota + 1
	// ErrCodeClockBackward 时钟回退：now 小于上一次被接受操作的 now。
	ErrCodeClockBackward
	// ErrCodeSanctionedPayee 收款人命中制裁。
	ErrCodeSanctionedPayee
	// ErrCodeIdempotencyConflict 同幂等键但参数不同。
	ErrCodeIdempotencyConflict
	// ErrCodeQuoteNotFound 报价不存在或已消耗。
	ErrCodeQuoteNotFound
	// ErrCodeQuoteExpired 报价已过期。
	ErrCodeQuoteExpired
	// ErrCodeSingleLimitExceeded 超过单笔限额。
	ErrCodeSingleLimitExceeded
	// ErrCodeDailyLimitExceeded 超过自然日限额。
	ErrCodeDailyLimitExceeded
	// ErrCodeAnnualLimitExceeded 超过滚动年度额度。
	ErrCodeAnnualLimitExceeded
	// ErrCodeTransferNotFound 汇款不存在。
	ErrCodeTransferNotFound
	// ErrCodeIllegalState 当前状态不允许该操作（含已逾期失败）。
	ErrCodeIllegalState
)

// Error 是本包返回的唯一具体错误类型。
type Error struct {
	Code ErrorCode
	msg  string
}

func (e *Error) Error() string { return e.msg }

func newError(code ErrorCode, format string, args ...any) error {
	return &Error{Code: code, msg: fmt.Sprintf(format, args...)}
}

// CodeOf 提取错误中的 ErrorCode；非本包错误返回 0。
func CodeOf(err error) ErrorCode {
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return 0
}
