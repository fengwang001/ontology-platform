// Package carpool 实现沿固定走廊运行的网约车合乘匹配与费用分摊服务。
//
// 走廊是一条直线，位置为非负整数，车辆只沿正向行驶。
// 所有金额以最小货币单位的整数表示，所有时刻为整数，
// 以保证每次匹配与每笔费用都可精确复现。
package carpool

import "errors"

// OrderStatus 是订单的生命周期状态。
type OrderStatus int

const (
	StatusWaiting   OrderStatus = iota // 等待匹配
	StatusMatched                      // 已匹配、未上车
	StatusOnboard                      // 已上车
	StatusCompleted                    // 已完成并结算
	StatusCancelled                    // 已取消
	StatusExpired                      // 超过最晚上车时刻仍未匹配
)

// String 返回状态的可读名称。
func (s OrderStatus) String() string {
	switch s {
	case StatusWaiting:
		return "Waiting"
	case StatusMatched:
		return "Matched"
	case StatusOnboard:
		return "Onboard"
	case StatusCompleted:
		return "Completed"
	case StatusCancelled:
		return "Cancelled"
	case StatusExpired:
		return "Expired"
	}
	return "Unknown"
}

// Config 是走廊级常量配置。
type Config struct {
	StopDuration    int64 // 每个停靠点的固定停靠时长
	TimePerDistance int64 // 每单位里程的行驶时间（速度的倒数，保持整数）
	UnitPrice       int64 // 每单位里程的基础费用（最小货币单位）
	CancelFee       int64 // 固定取消费
	MaxActiveOrders int   // 每辆车同时在途订单数上限
}

// Order 是一个合乘订单。
type Order struct {
	ID           string
	Pickup       int64
	Dropoff      int64
	Persons      int
	MaxStopDelay int64
	LatestPickup int64
	SubmitTime   int64
	Seq          int // 全局下单序号，用于“下单最早”判定
	Status       OrderStatus
	VehicleID    string
	Cap          int64 // 锁价上限；未匹配时为 0
	Settled      int64 // 已完成/已取消订单的最终应付
}

// Vehicle 是一辆只沿走廊正向行驶的车。
type Vehicle struct {
	ID     string
	Seats  int
	Pos    int64
	Active []*Order // 在途订单（已匹配未上车 + 已上车）
}

// SubmitResult 是下单结果。
type SubmitResult struct {
	Status    OrderStatus // StatusMatched / StatusOnboard / StatusWaiting
	VehicleID string      // 已匹配时的车辆标识
}

// EventKind 是位置更新产生的事件类型。
type EventKind int

const (
	EventMatched   EventKind = iota // 等待订单被匹配
	EventBoarded                    // 乘客上车
	EventCompleted                  // 乘客到达下车点并完成结算
	EventExpired                    // 等待订单失效
)

// String 返回事件类型的可读名称。
func (k EventKind) String() string {
	switch k {
	case EventMatched:
		return "Matched"
	case EventBoarded:
		return "Boarded"
	case EventCompleted:
		return "Completed"
	case EventExpired:
		return "Expired"
	}
	return "Unknown"
}

// Event 描述一次状态变化，用于日志与核对。
type Event struct {
	Kind      EventKind
	OrderID   string
	VehicleID string
	Fare      int64 // 完成事件的结算金额，其余为 0
}

// OrderView 是订单查询结果。
type OrderView struct {
	ID            string
	Status        OrderStatus
	VehicleID     string
	EstimatedFare int64 // 当前预计应付；已完成/已取消为最终应付
	Cap           int64 // 锁价上限；未匹配为 0
	SoloFare      int64 // 独行费用
}

// 错误分类，判定优先级按声明次序（参数非法最靠前）。
var (
	ErrInvalidParam       = errors.New("carpool: invalid parameter")
	ErrClockRollback      = errors.New("carpool: clock rollback")
	ErrVehicleNotFound    = errors.New("carpool: vehicle not found")
	ErrOrderNotFound      = errors.New("carpool: order not found")
	ErrOrderCompleted     = errors.New("carpool: order already completed")
	ErrOrderOnboard       = errors.New("carpool: order already onboard, cannot cancel")
	ErrPositionRegression = errors.New("carpool: position regression")
	ErrNoVehicle          = errors.New("carpool: no vehicle available")
)
