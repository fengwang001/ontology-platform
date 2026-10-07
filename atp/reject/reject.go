// Package reject 定义所有操作统一的拒绝原因与错误类型。
//
// 拒绝原因按如下优先级判定（高在前）：
// 参数非法 > 时钟回退 > 订单重复 > 永久缺货 > 暂时缺货 > 拆分过多。
// OrderNotFound 与 ReservationExpired 用于出库确认/释放预留，
// 不参与上述优先级排序。
package reject

import (
	"errors"
	"fmt"
)

// Reason 为拒绝原因类别。
type Reason int

const (
	// InvalidParam 参数非法（数量为负、时刻为负、订单行为空、查询时刻早于当前时刻等）。
	InvalidParam Reason = iota
	// ClockRollback 操作携带的时刻小于上一次被接受操作的时刻。
	ClockRollback
	// DuplicateOrder 同一订单号已存在仍有效的预留。
	DuplicateOrder
	// PermanentStockout 永久缺货：全部仓库现货加全部计划入库之和即不足。
	PermanentStockout
	// TemporaryStockout 暂时缺货：总量足够，但受预留或到货时刻限制不足。
	TemporaryStockout
	// TooManySplits 订单涉及的不同仓库数超过上限。
	TooManySplits
	// OrderNotFound 订单不存在（含预留已到期后被释放的情形）。
	OrderNotFound
	// ReservationExpired 确认出库时预留已到期。
	ReservationExpired
)

// Error 为带类别与上下文的拒绝错误。
type Error struct {
	Reason Reason
	// Line 为缺货订单行的下标（从 0 开始）；不适用于非缺货类错误时为 -1。
	Line int
	// Detail 为人类可读的判定依据说明。
	Detail string
}

func (e *Error) Error() string {
	if e.Line >= 0 {
		return fmt.Sprintf("%s: line %d: %s", e.Reason, e.Line, e.Detail)
	}
	return fmt.Sprintf("%s: %s", e.Reason, e.Detail)
}

// New 构造一个拒绝错误；line 不适用时传 -1。
func New(r Reason, line int, detail string) *Error {
	return &Error{Reason: r, Line: line, Detail: detail}
}

// Is 判断 err 是否为指定类别的拒绝。
func Is(err error, r Reason) bool {
	var re *Error
	if errors.As(err, &re) {
		return re.Reason == r
	}
	return false
}

// LineOf 返回缺货错误对应的订单行下标；非缺货错误返回 -1。
func LineOf(err error) int {
	var re *Error
	if errors.As(err, &re) {
		return re.Line
	}
	return -1
}

func (r Reason) String() string {
	switch r {
	case InvalidParam:
		return "invalid_param"
	case ClockRollback:
		return "clock_rollback"
	case DuplicateOrder:
		return "duplicate_order"
	case PermanentStockout:
		return "permanent_stockout"
	case TemporaryStockout:
		return "temporary_stockout"
	case TooManySplits:
		return "too_many_splits"
	case OrderNotFound:
		return "order_not_found"
	case ReservationExpired:
		return "reservation_expired"
	}
	return "unknown"
}
