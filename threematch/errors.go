// Package threematch 实现采购订单、收货与发票的三单匹配及付款放行。
//
// 所有业务错误均以 *Error 形式返回，Error.Kind 为结构化错误种类，
// 便于调用方按种类区分“参数非法 / 时钟回退 / 对象不存在 / 供应商冻结 /
// 状态不符 / 业务拒绝”等情形。
package threematch

import "fmt"

// ErrorKind 是结构化错误种类。拒绝优先级总体为：
//
//	参数非法 > 时钟回退 > 对象不存在 > 供应商冻结 > 状态不符 > 业务拒绝
type ErrorKind int

const (
	// KindInvalidParam 表示参数非法（数量、价格、时刻为负/零，引用越界等）。
	KindInvalidParam ErrorKind = iota + 1
	// KindClockRewind 表示操作时刻早于上一次被接受操作的时刻。
	KindClockRewind
	// KindNotFound 表示引用的供应商、订单、发票等对象不存在。
	KindNotFound
	// KindSupplierFrozen 表示供应商当前处于冻结状态。
	KindSupplierFrozen
	// KindInvalidState 表示对象当前状态不允许该操作（未放行即付款等）。
	KindInvalidState
	// KindDuplicate 表示发票号重复（已放行或被保留）。
	KindDuplicate
	// KindOverReceipt 表示累计收货超过订购量加超收容忍额。
	KindOverReceipt
	// KindReceiptOccupied 表示冲销会使累计收货低于已放行发票占用数量。
	KindReceiptOccupied
	// KindLineNotFound 表示发票行引用的订单行不存在。
	KindLineNotFound
	// KindSupplierMismatch 表示发票行所属订单的供应商与发票供应商不一致。
	KindSupplierMismatch
	// KindPriceMismatch 表示发票单价与订单单价之差超过价格容差额。
	KindPriceMismatch
	// KindOverInvoiced 表示发票累计数量超过累计收货量（超开）。
	KindOverInvoiced
	// KindAlreadyPaid 表示发票已付款而再次付款。
	KindAlreadyPaid
)

// kindPriority 为错误种类对应的总体拒绝优先级，数值越小优先级越高。
var kindPriority = map[ErrorKind]int{
	KindInvalidParam:     1,
	KindClockRewind:      2,
	KindNotFound:         3,
	KindSupplierFrozen:   4,
	KindInvalidState:     5,
	KindDuplicate:        6,
	KindOverReceipt:      6,
	KindReceiptOccupied:  6,
	KindLineNotFound:     6,
	KindSupplierMismatch: 6,
	KindPriceMismatch:    6,
	KindOverInvoiced:     6,
	KindAlreadyPaid:      6,
}

// Priority 返回错误种类的总体拒绝优先级（1 最高）。
func (k ErrorKind) Priority() int {
	if p, ok := kindPriority[k]; ok {
		return p
	}
	return 100
}

func (k ErrorKind) String() string {
	switch k {
	case KindInvalidParam:
		return "参数非法"
	case KindClockRewind:
		return "时钟回退"
	case KindNotFound:
		return "对象不存在"
	case KindSupplierFrozen:
		return "供应商冻结"
	case KindInvalidState:
		return "状态不符"
	case KindDuplicate:
		return "发票号重复"
	case KindOverReceipt:
		return "超收"
	case KindReceiptOccupied:
		return "已被发票占用"
	case KindLineNotFound:
		return "订单行不存在"
	case KindSupplierMismatch:
		return "供应商不一致"
	case KindPriceMismatch:
		return "价格不符"
	case KindOverInvoiced:
		return "超开"
	case KindAlreadyPaid:
		return "已付款"
	default:
		return "未知错误"
	}
}

// Error 是三单匹配系统返回的结构化错误。
type Error struct {
	Kind ErrorKind
	Msg  string
}

func (e *Error) Error() string {
	return e.Kind.String() + ": " + e.Msg
}

func newError(kind ErrorKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}
