// Package ontology 实现供应商寄售库存的领用结算系统。
package ontology

// ErrorKind 描述可区分的错误类别。
type ErrorKind string

const (
	// KindInvalidParam 参数非法。
	KindInvalidParam ErrorKind = "invalid_param"
	// KindClockRollback 时钟回退。
	KindClockRollback ErrorKind = "clock_rollback"
	// KindNotFound 对象不存在。
	KindNotFound ErrorKind = "not_found"
	// KindNoValidPrice 无有效价格。
	KindNoValidPrice ErrorKind = "no_valid_price"
	// KindInsufficientStock 库存不足。
	KindInsufficientStock ErrorKind = "insufficient_stock"
	// KindOverCap 超上限。
	KindOverCap ErrorKind = "over_cap"
	// KindExcessQuantity 数量过量（退回/冲销的数量越界）。
	KindExcessQuantity ErrorKind = "excess_quantity"
	// KindPeriodOpen 对账单周期尚未结束。
	KindPeriodOpen ErrorKind = "period_open"
	// KindPriceOverlap 价格协议生效区间重叠。
	KindPriceOverlap ErrorKind = "price_overlap"
)

// Error 携带可区分类别的错误。
type Error struct {
	Kind ErrorKind
	Msg  string
}

func (e *Error) Error() string { return string(e.Kind) + ": " + e.Msg }

func newErr(kind ErrorKind, msg string) *Error {
	return &Error{Kind: kind, Msg: msg}
}

// IsError 判断 err 是否为指定类别的本体错误。
func IsError(err error, kind ErrorKind) bool {
	if e, ok := err.(*Error); ok {
		return e.Kind == kind
	}
	return false
}
