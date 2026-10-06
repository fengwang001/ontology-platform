package dispatch

import "errors"

// 拒绝次序固定为：参数非法 -> 时钟回退 -> 对象不存在/状态不符 -> 无可行骑手（三种细分）。
var (
	ErrInvalidArgument = errors.New("dispatch: invalid argument")
	ErrClockRollback   = errors.New("dispatch: clock rollback")

	ErrOrderNotFound        = errors.New("dispatch: order not found")
	ErrRiderNotFound        = errors.New("dispatch: rider not found")
	ErrRiderOffline         = errors.New("dispatch: rider offline")
	ErrOrderExists          = errors.New("dispatch: order already exists")
	ErrOrderAlreadyAssigned = errors.New("dispatch: order already assigned")
	ErrOrderNotAssigned     = errors.New("dispatch: order not assigned")
	ErrOrderAlreadyPicked   = errors.New("dispatch: order already picked")
	ErrOrderNotAtRider      = errors.New("dispatch: order not assigned to rider")
	ErrStopOutOfOrder       = errors.New("dispatch: stop completion out of order")
	ErrCancelWouldDelay     = errors.New("dispatch: cancellation would delay an in-transit order")

	ErrNoRiderRegionCapacity = errors.New("dispatch: no in-region rider with free capacity")
	ErrNewOrderPromise       = errors.New("dispatch: no insertion satisfies the new order promise")
	ErrExistingViolation     = errors.New("dispatch: satisfying new order violates existing promise or detour cap")
)
