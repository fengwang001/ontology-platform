package fulfillment

import "errors"

// 各类错误均可通过 errors.Is 程序化区分。
var (
	ErrInvalidParam   = errors.New("fulfillment: invalid parameter")
	ErrClockRollback  = errors.New("fulfillment: clock rollback")
	ErrOrderNotFound  = errors.New("fulfillment: order not found")
	ErrOrderExists    = errors.New("fulfillment: order already exists")
	ErrOrderCancelled = errors.New("fulfillment: order cancelled")
	ErrEventOrder     = errors.New("fulfillment: event order violation")
	ErrAddrChanged    = errors.New("fulfillment: address already changed")
	ErrAlreadyPaid    = errors.New("fulfillment: order already paid")
	ErrNotDelivered   = errors.New("fulfillment: order not delivered")
	ErrWindowExpired  = errors.New("fulfillment: claim window expired")
	ErrWindowNotEnded = errors.New("fulfillment: claim window not ended")
	ErrNoDelay        = errors.New("fulfillment: no delay")
	ErrNotTopTier     = errors.New("fulfillment: delay not in top tier")
)
