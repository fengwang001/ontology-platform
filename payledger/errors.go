package payledger

import "fmt"

// ErrKind 区分错误类别。数值越小优先级越高：
// 同一操作触发多个错误条件时，只报告优先级最高（数值最小）的第一个。
type ErrKind int

const (
	// ErrInvalidParam 参数非法（金额非正、编号为空、账户已存在等）。优先级最高。
	ErrInvalidParam ErrKind = iota
	// ErrClockRollback 时钟回退：操作携带的 now 小于上一次被接受操作的 now。
	ErrClockRollback
	// ErrDuplicateAuthID 授权编号重复（全局唯一，含已终结授权）。位于时钟回退之后、其余检查之前。
	ErrDuplicateAuthID
	// ErrAuthNotFound 授权不存在。
	ErrAuthNotFound
	// ErrAccountNotFound 卡账户不存在。
	ErrAccountNotFound
	// ErrAuthTerminated 授权已终结（已撤销、已过期、已终捕）。
	ErrAuthTerminated
	// ErrOverTolerance 累计捕获额超过累计授权额按容差基点上浮后的上限。
	ErrOverTolerance
	// ErrInsufficientFunds 可用额度不足。
	ErrInsufficientFunds
	// ErrRefundExceeds 退款额超过该授权可退余额（累计已捕获减累计已退款）。
	ErrRefundExceeds
)

// Error 是账本返回的唯一错误类型，Kind 用于区分类别与判定优先级。
type Error struct {
	Kind ErrKind
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("payledger: %s", e.Msg) }

func newErr(kind ErrKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}
