package inventory

import "fmt"

// Code 标识操作被拒绝的原因类别，拒绝优先级（高到低）：
// 参数非法 > 时钟回退 > 订单重复 > 永久缺货 > 暂时缺货 > 拆分过多。
type Code int

const (
	CodeOK Code = iota
	CodeInvalidParam
	CodeClockRollback
	CodeOrderDuplicate
	CodePermanentShortage
	CodeTemporaryShortage
	CodeTooManySplits
	CodeOrderNotFound
	CodeReservationExpired
	CodeInboundNotFound
	// CodeInsufficientOnHand 是防御性错误：按系统设计的不变量不会触发，
	// 仅用于保证任何情况下现货都不为负。
	CodeInsufficientOnHand
)

// Error 是系统返回的唯一错误类型，携带类别码与缺货行下标。
type Error struct {
	Code Code
	// Line 仅对缺货类错误有效，表示缺货订单行的下标（从 0 开始），其余为 -1。
	Line int
	msg  string
}

func (e *Error) Error() string { return e.msg }

func newError(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Line: -1, msg: fmt.Sprintf(format, args...)}
}

func shortageError(code Code, line int, product string) *Error {
	kind := "永久缺货"
	if code == CodeTemporaryShortage {
		kind = "暂时缺货"
	}
	e := newError(code, "订单行 %d（商品 %s）%s", line, product, kind)
	e.Line = line
	return e
}

// CodeOf 提取错误的类别码；err 为 nil 时返回 CodeOK。
func CodeOf(err error) Code {
	if err == nil {
		return CodeOK
	}
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return CodeInvalidParam
}

// LineOf 提取缺货错误对应的订单行下标，其余情况返回 -1。
func LineOf(err error) int {
	if e, ok := err.(*Error); ok {
		return e.Line
	}
	return -1
}
