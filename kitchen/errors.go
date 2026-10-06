package kitchen

import "errors"

// 各类错误均为可程序化区分的哨兵错误，调用方用 errors.Is 判定。
// 每个操作只报第一个命中的错误，判定次序：
// 参数非法、时钟回退、订单不存在或已存在、状态类错误、商家暂停、预约过近、爆单。
var (
	// ErrInvalidArgument 参数非法（构造参数越界、时长非正、时刻为负、完成时刻早于开工时刻等）。
	ErrInvalidArgument = errors.New("kitchen: 参数非法")
	// ErrClockRegression 时钟回退：操作携带的时刻早于此前被接受操作的最大时刻。
	ErrClockRegression = errors.New("kitchen: 时钟回退")
	// ErrOrderNotFound 订单不存在（含已取消）。
	ErrOrderNotFound = errors.New("kitchen: 订单不存在")
	// ErrOrderExists 订单已存在（同一商家内订单 ID 不可复用）。
	ErrOrderExists = errors.New("kitchen: 订单已存在")
	// ErrNotStarted 对未开工订单报告完成。
	ErrNotStarted = errors.New("kitchen: 订单未开工")
	// ErrAlreadyStarted 取消已开工订单。
	ErrAlreadyStarted = errors.New("kitchen: 订单已开工")
	// ErrAlreadyCompleted 对已完成订单再次完成或取消。
	ErrAlreadyCompleted = errors.New("kitchen: 订单已完成")
	// ErrMerchantPaused 商家暂停期间提交新即时单或新预约单。
	ErrMerchantPaused = errors.New("kitchen: 商家暂停")
	// ErrReservationTooSoon 预约单目标开工时刻早于 当前时刻+预约单提前量。
	ErrReservationTooSoon = errors.New("kitchen: 预约过近")
	// ErrBurst 预计等待不小于爆单阈值，拒单。
	ErrBurst = errors.New("kitchen: 爆单")
)
