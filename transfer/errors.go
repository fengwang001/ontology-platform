// Package transfer 实现仓库之间库存调拨的在途管理。
package transfer

import "errors"

// 拒绝优先级（自上而下）：
//  1. 参数非法      ErrInvalidParam / ErrDuplicateOrder
//  2. 时钟回退      ErrClockRegression
//  3. 调拨单不存在  ErrOrderNotFound
//  4. 状态不符      ErrOrderNotShipped / ErrOrderAlreadyShipped / ErrOrderClosed /
//     ErrOrderCancelled / ErrOrderNotClosed
//  5. 业务拒绝      ErrInsufficientStock / ErrOverReceive / ErrNotClosableYet /
//     ErrRecoverExcess / ErrNoShortage
var (
	ErrInvalidParam   = errors.New("transfer: invalid parameter")
	ErrDuplicateOrder = errors.New("transfer: duplicate order id")

	ErrClockRegression = errors.New("transfer: clock regression")

	ErrOrderNotFound = errors.New("transfer: order not found")

	ErrOrderNotShipped     = errors.New("transfer: order not shipped")
	ErrOrderAlreadyShipped = errors.New("transfer: order already shipped")
	ErrOrderClosed         = errors.New("transfer: order closed")
	ErrOrderCancelled      = errors.New("transfer: order cancelled")
	ErrOrderNotClosed      = errors.New("transfer: order not closed")

	ErrInsufficientStock = errors.New("transfer: insufficient available stock")
	ErrOverReceive       = errors.New("transfer: over-receipt beyond tolerance")
	ErrNotClosableYet    = errors.New("transfer: close wait time not reached")
	ErrRecoverExcess     = errors.New("transfer: recovery exceeds shortage")
	ErrNoShortage        = errors.New("transfer: line has no shortage")
)

// InsufficientStockError 携带下标最小的库存不足行信息。
type InsufficientStockError struct {
	LineIndex int
	Product   string
	Want      int64
	Have      int64
}

func (e *InsufficientStockError) Error() string {
	return ErrInsufficientStock.Error()
}

func (e *InsufficientStockError) Is(target error) bool { return target == ErrInsufficientStock }

// OverReceiveError 携带超收行的容忍上限。
type OverReceiveError struct {
	LineIndex int
	Limit     int64
	Attempt   int64
}

func (e *OverReceiveError) Error() string {
	return ErrOverReceive.Error()
}

func (e *OverReceiveError) Is(target error) bool { return target == ErrOverReceive }

// NotClosableYetError 携带最早可关闭时刻。
type NotClosableYetError struct {
	Now      int64
	Earliest int64
}

func (e *NotClosableYetError) Error() string {
	return ErrNotClosableYet.Error()
}

func (e *NotClosableYetError) Is(target error) bool { return target == ErrNotClosableYet }

// RecoverExcessError 携带该行当前短缺上限。
type RecoverExcessError struct {
	LineIndex int
	Max       int64
	Attempt   int64
}

func (e *RecoverExcessError) Error() string {
	return ErrRecoverExcess.Error()
}

func (e *RecoverExcessError) Is(target error) bool { return target == ErrRecoverExcess }
