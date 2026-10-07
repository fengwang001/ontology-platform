// Package settlement 实现支付商户的延迟结算与滚动保证金系统。
package settlement

import "fmt"

// Code 错误码，用于区分各类被拒绝的操作。
type Code int

const (
	// CodeInvalidParam 参数非法（空编号、N/H 小于 1、基点越界等）。
	CodeInvalidParam Code = iota
	// CodeClockRollback 时钟回退：now 小于上一次被接受操作的 now。
	CodeClockRollback
	// CodeMerchantNotFound 商户不存在。
	CodeMerchantNotFound
	// CodeMerchantExists 商户已存在（注册时）。
	CodeMerchantExists
	// CodeDuplicateTxID 流水编号重复（同一商户内）。
	CodeDuplicateTxID
	// CodeInvalidDate 流水发生日晚于当前 now。
	CodeInvalidDate
	// CodeAlreadyClosed 流水发生日早于商户已结算到的最近营业日（已封账）。
	CodeAlreadyClosed
	// CodeNonBusinessDay 结算截止日不是营业日。
	CodeNonBusinessDay
	// CodeDuplicateSettlement 结算截止日不晚于上次结算到的营业日。
	CodeDuplicateSettlement
)

// Error 是可区分错误类型的载体，调用方可用 errors.As 取出并比较 Code。
type Error struct {
	Code Code
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("settlement: code=%d %s", e.Code, e.Msg) }

func newError(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}
